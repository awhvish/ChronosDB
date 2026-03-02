package raft

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
)

/*
	Custom Raft WAL.
	Appendable entries, drain loop driven batching and strict binary encoding for fast low-memory data persistence.
*/

// Global pool to reuse buffers
var walBufPool = sync.Pool{
	New: func() interface{} {
		return bytes.NewBuffer(make([]byte, 0, 16*1024))
	},
}

const (
	RecordTypeLog       = 0x00
	RecordTypeHardState = 0x01
)

type HardState struct {
	Term   uint32
	Vote   uint32
	Commit uint32
}

type WALEntry struct {
	RecordType byte
	Index      uint32
	Term       uint32
	Command    []byte
}

type WAL struct {
	file          *os.File
	writer        *bufio.Writer
	mu            sync.Mutex
	triggerCh     chan struct{}
	lastPersisted uint32
}

func createOrOpenRaftWAL(nodeId int) (*WAL, error) {
	filename := fmt.Sprintf("raf_wal_%d", nodeId)
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		return nil, err
	}
	writer := bufio.NewWriterSize(f, 64*1024)

	wal := &WAL{
		file:          f,
		writer:        writer,
		lastPersisted: 0, // dummy value
		triggerCh:     make(chan struct{}, 1),
	}
	go wal.backgroundSyncer()
	return wal, nil
}

func (w *WAL) sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Sync()
}

// backgroundSyncer drain loop driven accumulation
func (w *WAL) backgroundSyncer() {
	for {
		<-w.triggerCh
	drain:
		for {
			select {
			case <-w.triggerCh:
			default:
				break drain
			}
		}
		_ = w.sync()
	}
}

func (w *WAL) AppendEntries(entries []WALEntry, startIndex int) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	lastLogIdx := uint32(startIndex + len(entries) - 1)

	if lastLogIdx <= w.lastPersisted || len(entries) == 0 {
		return nil
	}

	// We only want entries starting after lastPersisted. Math: (lastPersisted - startIndex) + 1
	startOffset := 0
	if uint32(startIndex) <= w.lastPersisted {
		startOffset = int(w.lastPersisted - uint32(startIndex) + 1)
	}

	newEntries := entries[startOffset:]

	buf := walBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer walBufPool.Put(buf)

	headerScratch := make([]byte, 12)
	highestIndex := w.lastPersisted

	for _, entry := range newEntries {
		idx := entry.Index

		if idx <= w.lastPersisted {
			continue
		}
		if err := buf.WriteByte(RecordTypeLog); err != nil {
			return err
		}

		// Write entry header: Index(4) + Term(4) + CmdLen(4)
		binary.LittleEndian.PutUint32(headerScratch[0:4], entry.Index)
		binary.LittleEndian.PutUint32(headerScratch[4:8], entry.Term)
		binary.LittleEndian.PutUint32(headerScratch[8:12], uint32(len(entry.Command)))

		// Write header
		buf.Write(headerScratch)
		buf.Write(entry.Command)
		highestIndex = idx
	}

	if buf.Len() > 0 {
		if _, err := w.writer.Write(buf.Bytes()); err != nil {
			return err
		}
		w.lastPersisted = highestIndex

		if err := w.writer.Flush(); err != nil {
			return err
		}
		w.lastPersisted = highestIndex
		select {
		case w.triggerCh <- struct{}{}:
		default:
		}
	}
	return nil
}

// PersistHardState writes the hard state (term, vote, commit) to WAL
func (w *WAL) PersistHardState(term, vote, commit uint32) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	buf := walBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer walBufPool.Put(buf)

	if err := buf.WriteByte(RecordTypeHardState); err != nil {
		return err
	}

	// Write hard state: Term(4) + Vote(4) + Commit(4)
	headerScratch := make([]byte, 12)
	binary.LittleEndian.PutUint32(headerScratch[0:4], term)
	binary.LittleEndian.PutUint32(headerScratch[4:8], vote)
	binary.LittleEndian.PutUint32(headerScratch[8:12], commit)
	buf.Write(headerScratch)

	if _, err := w.writer.Write(buf.Bytes()); err != nil {
		return err
	}

	if err := w.writer.Flush(); err != nil {
		return err
	}

	select {
	case w.triggerCh <- struct{}{}:
	default:
	}

	return nil
}

// TruncateWAL clears the existing WAL, starts a fresh one, and writes the current HardState.
// This is used after a snapshot is created to discard old logs.
func (w *WAL) TruncateWAL(lastIncludedIndex uint32, term, vote, commit uint32) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 1. Close and cleanup old file
	if err := w.writer.Flush(); err != nil {
		fmt.Printf("Warning: error flushing before truncate: %v\n", err)
	}
	w.file.Close()

	filename := w.file.Name()
	if err := os.Remove(filename); err != nil {
		return fmt.Errorf("failed to remove old WAL: %w", err)
	}

	// 2. Re-open fresh file
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("failed to open new WAL: %w", err)
	}
	w.file = f
	w.writer = bufio.NewWriterSize(f, 64*1024)
	w.lastPersisted = lastIncludedIndex

	// 3. Write HardState immediately
	buf := walBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer walBufPool.Put(buf)

	if err := buf.WriteByte(RecordTypeHardState); err != nil {
		return err
	}

	headerScratch := make([]byte, 12)
	binary.LittleEndian.PutUint32(headerScratch[0:4], term)
	binary.LittleEndian.PutUint32(headerScratch[4:8], vote)
	binary.LittleEndian.PutUint32(headerScratch[8:12], commit)
	buf.Write(headerScratch)

	if _, err := w.writer.Write(buf.Bytes()); err != nil {
		return err
	}

	if err := w.writer.Flush(); err != nil {
		return err
	}

	return nil
}

// RecoverEntries Returns recovered entries, hard states, errors
func (w *WAL) RecoverEntries() ([]WALEntry, HardState, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.file.Seek(0, 0); err != nil {
		return nil, HardState{}, err
	}

	var entries []WALEntry
	var state HardState

	reader := bufio.NewReader(w.file)
	header := make([]byte, 12)

	for {
		// Check: Entry or HardState
		typeByte, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				break
			}
			return entries, state, err
		}

		switch typeByte {
		// Append into entries for Logs
		case RecordTypeLog:
			if _, err := io.ReadFull(reader, header); err != nil {
				return entries, state, err
			}
			idx := binary.LittleEndian.Uint32(header[0:4])
			term := binary.LittleEndian.Uint32(header[4:8])
			cmdLen := binary.LittleEndian.Uint32(header[8:12])

			cmd := make([]byte, cmdLen)

			if _, err := io.ReadFull(reader, cmd); err != nil {
				return entries, state, err
			}
			entries = append(entries, WALEntry{RecordTypeLog, idx, term, cmd})

		// Update states for Hard State
		case RecordTypeHardState:
			if _, err := io.ReadFull(reader, header); err != nil {
				return entries, state, err
			}
			state.Term = binary.LittleEndian.Uint32(header[0:4])
			state.Vote = binary.LittleEndian.Uint32(header[4:8])
			state.Commit = binary.LittleEndian.Uint32(header[8:12])
		}
	}

	if len(entries) > 0 {
		w.lastPersisted = entries[len(entries)-1].Index
	}

	return entries, state, nil

}
