package kv

import (
	"KV-Store/pkg/arena"
	"KV-Store/pkg/wal"
	pb "KV-Store/proto"
	"KV-Store/raft"
	"KV-Store/sstable"
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Pool for reusing OpResult channels
var opResultPool = sync.Pool{
	New: func() interface{} {
		return make(chan OpResult, 1)
	},
}

type MemTable struct {
	Index map[string]int
	Arena *arena.Arena
	Size  uint32
	Wal   *wal.WAL
}

type Store struct {
	ActiveMap *MemTable
	frozenMap *MemTable
	ssTables  []*sstable.Reader
	WalDir    string
	SstDir    string
	walSeq    int64
	FlushChan chan struct{} // FrozenMem -> Active Mem
	Me        int           // same as raft.me, for prometheus metrics

	// Raft Channels
	Raft         *raft.Raft
	notifyChans  map[int]chan OpResult // return client -> success
	applyCh      chan raft.ApplyMsg    // applied cmds -> internal storage
	mu           sync.RWMutex
	compactionMu sync.Mutex
	cond         *sync.Cond
}

func NewKVStore(peers []pb.RaftServiceClient, me int) (*Store, error) {
	walDir := fmt.Sprintf("Storage/wal/wal_%d", me)
	sstDir := fmt.Sprintf("Storage/data/data_%d", me)

	// 2. Create them if they don't exist
	if err := os.MkdirAll(walDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create wal dir: %w", err)
	}
	if err := os.MkdirAll(sstDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sst dir: %w", err)
	}
	_, seqId, _ := wal.FindActiveFile(walDir)

	currentWal, _ := wal.OpenWAL(walDir, seqId)
	entries, _ := currentWal.Recover()
	applyCh := make(chan raft.ApplyMsg)
	store := &Store{
		ActiveMap: NewMemTable(mapLimit, currentWal),
		frozenMap: nil,
		WalDir:    walDir,
		SstDir:    sstDir,
		FlushChan: make(chan struct{}, 1),
		applyCh:   applyCh,
		Me:        me,
	}
	store.cond = sync.NewCond(&store.mu)
	for _, entry := range entries {
		k := string(entry.Key)
		v := string(entry.Value)

		var offset int
		var err error
		switch entry.Cmd {
		case wal.CmdPut:
			offset, err = store.ActiveMap.Arena.Put(k, v, false)
			if err != nil {
				return nil, err
			}
		case wal.CmdDelete:
			offset, err = store.ActiveMap.Arena.Put(k, v, true) //handles tombstone
			if err != nil {
				return nil, err
			}
		}
		store.ActiveMap.Index[k] = offset
		store.ActiveMap.Size += uint32(len(k) + len(v))
	}
	store.refreshSSTables()
	store.Raft = raft.Make(peers, me, applyCh)
	go store.readAppliedLogs()
	go store.FlushWorker()
	return store, nil
}

// Loop that pulls data from Raft and writes to Store
func (s *Store) readAppliedLogs() {
	for msg := range s.applyCh {
		if msg.SnapshotValid {
			s.restoreSnapshot(msg.Snapshot)
			continue
		}

		if !msg.CommandValid {
			continue
		}

		var cmd raftCmd
		if err := json.Unmarshal(msg.Command, &cmd); err != nil {
			continue
		}

		var err error
		if cmd.Op == CmdPut {
			err = s.applyInternal(cmd.Key, cmd.Value, false)
		} else if cmd.Op == CmdDelete {
			err = s.applyInternal(cmd.Key, "", true)
		}

		s.mu.Lock()
		// We check if any client is waiting for this specific log index
		if ch, ok := s.notifyChans[msg.CommandIndex]; ok {
			ch <- OpResult{
				Value: cmd.Value,
				Err:   err,
			}
			delete(s.notifyChans, msg.CommandIndex)
		}
		s.mu.Unlock()

		// Trigger snapshot if log grows too big
		if msg.CommandIndex%1000 == 0 {
			go s.takeSnapshot(msg.CommandIndex)
		}
	}
}

func (s *Store) takeSnapshot(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Capture state
	// Currently only snapshot ActiveMap.
	data := make(map[string]string)
	for k, v := range s.ActiveMap.Index {
		val, _, err := s.ActiveMap.Arena.Get(v) // Assuming Arena has Get(offset)
		if err == nil {
			data[k] = string(val)
		}
	}

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(data); err != nil {
		fmt.Printf("Snapshot encode error: %v\n", err)
		return
	}

	s.Raft.Snapshot(index, buf.Bytes())
}

// Put in storage
func (s *Store) applyInternal(key string, val string, isDelete bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	//  size: Header(1) + KeyLen(2) + ValLen(4) + Key + Val
	entrySize := 1 + 2 + 4 + len(key) + len(val)
	if int(s.ActiveMap.Size)+entrySize > mapLimit {
		if s.frozenMap != nil {
			return errors.New("write stall: memTable flushing")
		}
		s.RotateTable()
	}

	// Write in logs
	/*
		var er error
		if isDelete {
			er = s.activeMap.Wal.Write(key, val, wal.CmdDelete)
		} else {
			er = s.activeMap.Wal.Write(key, val, wal.CmdPut)

		}
		if er != nil {
			fmt.Println("Error writing log: ", er)
		}

	*/
	offset, err := s.ActiveMap.Arena.Put(key, val, isDelete)
	if err != nil {
		return errors.New("failed to put key " + key + ":" + err.Error())
	}
	s.ActiveMap.Index[key] = offset
	s.ActiveMap.Size += uint32(entrySize)
	return nil
}

func (s *Store) Put(key string, val string, isDelete bool) error {
	op := CmdPut
	if isDelete {
		op = CmdDelete
	}
	cmd := raftCmd{Op: op, Key: key, Value: val}
	cmdBytes, _ := json.Marshal(cmd)

	index, _, isLeader := s.Raft.Start(cmdBytes)
	if !isLeader {
		return fmt.Errorf("not leader")
	}

	// Get channel from pool
	ch := opResultPool.Get().(chan OpResult)

	s.mu.Lock()
	if s.notifyChans == nil {
		s.notifyChans = make(map[int]chan OpResult)
	}
	s.notifyChans[index] = ch
	s.mu.Unlock()

	// wait for consensus to replicate
	var result error
	select {
	case res := <-ch:
		result = res.Err
	case <-time.After(2 * time.Second):
		s.mu.Lock()
		delete(s.notifyChans, index)
		s.mu.Unlock()
		result = fmt.Errorf("timeout waiting for consensus")
	}

	// Return channel to pool
	select {
	case <-ch: // drain if not empty
	default:
	}
	opResultPool.Put(ch)

	return result
}

func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	// 1. Check active table
	if val, isTomb, found := checkTable(s.ActiveMap, key); found {
		s.mu.RUnlock()
		if isTomb {
			return "", false
		}
		return val, true
	}
	// 2. Check frozen table
	if val, isTomb, found := checkTable(s.frozenMap, key); found {
		s.mu.RUnlock()
		if isTomb {
			return "", false
		}
		return val, true
	}
	s.mu.RUnlock() // Unlock BEFORE Disk IO to avoid blocking writes!

	// 3. Check SSTables (Disk)
	files, _ := os.ReadDir(s.SstDir)
	var sstFiles []string
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".sst") {
			sstFiles = append(sstFiles, f.Name())
		}
	}
	// Sort reverse to check newest files first (level0_105.sst before level0_100.sst)
	sort.Sort(sort.Reverse(sort.StringSlice(sstFiles)))

	for _, file := range sstFiles {
		// Open the reader
		fullPath := filepath.Join(s.SstDir, file)
		reader, err := sstable.OpenSSTable(fullPath)
		if err != nil {
			continue // Skip bad files
		}

		// Search
		val, isTomb, found, err := reader.Get(key)
		_ = reader.Close()

		if err != nil {
			continue
		}

		if found {
			if isTomb {
				return "", false
			}
			return val, true
		}
	}
	return "", false
}

// restoreSnapshot clears the current state and repopulates it from the snapshot
func (s *Store) restoreSnapshot(snapshot []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var data map[string]string

	buf := bytes.NewBuffer(snapshot)
	dec := gob.NewDecoder(buf)
	if err := dec.Decode(&data); err != nil {
		fmt.Printf("Error decoding snapshot: %v\n", err)
		return
	}

	// Clear current state
	s.ActiveMap = nil
	s.frozenMap = nil
	s.ssTables = nil 
	s.walSeq = time.Now().UnixNano()

	newWal, err := wal.OpenWAL(s.WalDir, s.walSeq)
	if err != nil {
		fmt.Printf("Error creating new WAL during restore: %v\n", err)
		return
	}

	s.ActiveMap = NewMemTable(mapLimit, newWal)
	s.frozenMap = nil
	s.ssTables = nil 

	// Populate ActiveMap
	for k, v := range data {
		offset, err := s.ActiveMap.Arena.Put(k, v, false)
		if err != nil {
			fmt.Printf("Error restoring key %s: %v\n", k, err)
			continue
		}
		s.ActiveMap.Index[k] = offset
		s.ActiveMap.Size += uint32(len(k) + len(v) + 7) // approx overhead

		// Rotate if needed
		if s.ActiveMap.Size > mapLimit {
			// Rotate
			s.rotateTableLocked()
		}
	}

	fmt.Printf("Snapshot restored successfully. Keys: %d\n", len(data))
}

// Helper to rotate table while holding lock
func (s *Store) rotateTableLocked() {
	if s.frozenMap != nil {
		// Stall
		fmt.Println("Restore stall: waiting for flush")
		return
	}

	s.frozenMap = s.ActiveMap
	// Create new Active
	s.walSeq++
	newWal, _ := wal.OpenWAL(s.WalDir, s.walSeq)
	s.ActiveMap = NewMemTable(mapLimit, newWal)

	select {
	case s.FlushChan <- struct{}{}:
	default:
	}
}
