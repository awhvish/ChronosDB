package raft

import (
	"KV-Store/pkg/metrics"
	pb "KV-Store/proto"
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"
)

type State int

const (
	Follower State = iota
	Candidate
	Leader
)

// LogEntry represents a single entry in the Raft log
type LogEntry struct {
	Index   int
	Term    int
	Command []byte
}

// ApplyMsg is sent to the service (KVStore) when a log entry is committed or a snapshot is installed.
type ApplyMsg struct {
	CommandValid bool
	Command      []byte
	CommandIndex int

	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

type Raft struct {
	mu        sync.Mutex
	peers     []pb.RaftServiceClient // RPC clients to talk to other nodes
	me        int
	leaderId  int
	applyCh   chan ApplyMsg
	triggerCh chan struct{}
	commitCh  chan struct{}

	//persistent states
	currentTerm       int
	votedFor          int
	log               []LogEntry
	wal               *WAL
	lastIncludedIndex int // Index of the last entry included in the snapshot
	lastIncludedTerm  int // Term of the last entry included in the snapshot
	snapshotFile      *os.File

	//volatile state on all servers
	commitIndex int // index of highest log entry known to be committed
	lastApplied int // index of highest log entry applied to state machine

	//volatile state on leaders
	nextIndex  map[int]int
	matchIndex map[int]int

	state         State
	lastResetTime time.Time //last time we heard from a leader
}

func (rf *Raft) getState() (int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.state == Leader
}

func (rf *Raft) GetLeader() int {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.leaderId
}

// getLastLogIndex returns index of the last log in the snapshot
func (rf *Raft) getLastLogIndex() int {
	return rf.lastIncludedIndex + len(rf.log) - 1
}

// getLastLogTerm returns the term of the last log entry written to the snapshot
func (rf *Raft) getLastLogTerm() int {
	if len(rf.log) == 0 {
		// If the log is empty, return the term of the last included entry (i.e. the term in the last snapshot)
		return rf.lastIncludedTerm
	}
	return rf.log[len(rf.log)-1].Term
}

// getLastLogEntry returns the last log entry written to the snapshot
func (rf *Raft) getLastLogEntry() LogEntry {
	if len(rf.log) == 0 {
		// If the log is empty, return a dummy entry with the last included index and term (we can't guess the command because we just have the snapshot)
		return LogEntry{Index: rf.lastIncludedIndex, Term: rf.lastIncludedTerm}
	}
	return rf.log[len(rf.log)-1]
}

func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.state != Leader {
		return -1, rf.currentTerm, false
	}

	//create log entry
	index := rf.getLastLogIndex() + 1
	term := rf.currentTerm
	// TODO: Use type assertion or serialization later here
	cmdBytes, ok := command.([]byte)
	if !ok {
		return -1, rf.currentTerm, false
	}
	rf.log = append(rf.log, LogEntry{index, term, cmdBytes})

	//trigger replication
	select {
	case rf.triggerCh <- struct{}{}:
	default:
	}
	return index, term, true
}

// goroutine that pushes data into the KV Store
func (rf *Raft) applier() {
	for range rf.commitCh {
		rf.mu.Lock()
		if rf.commitIndex <= rf.lastApplied {
			rf.mu.Unlock()
			continue
		}

		// Snapshot all ready entries into a local slice
		entriesToApply := make([]LogEntry, 0, rf.commitIndex-rf.lastApplied)
		for rf.lastApplied < rf.commitIndex {
			rf.lastApplied++
			entriesToApply = append(entriesToApply, rf.log[rf.lastApplied])
		}
		rf.mu.Unlock()

		for _, entry := range entriesToApply {
			rf.applyCh <- ApplyMsg{
				CommandValid: true,
				Command:      entry.Command,
				CommandIndex: entry.Index,
			}
		}
	}
}

func Make(peers []pb.RaftServiceClient, me int, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.me = me
	rf.applyCh = applyCh
	rf.triggerCh = make(chan struct{}, 1)
	rf.commitCh = make(chan struct{}, 1)
	rf.state = Follower
	rf.currentTerm = 0
	rf.votedFor = -1
	rf.leaderId = -1
	rf.log = make([]LogEntry, 0)
	rf.lastIncludedIndex = 0
	rf.lastIncludedTerm = 0

	rf.commitIndex = 0
	rf.lastApplied = 0

	rf.nextIndex = make(map[int]int)
	rf.matchIndex = make(map[int]int)

	// Initialize WAL
	wal, err := createOrOpenRaftWAL(me)
	if err != nil {
		fmt.Printf("Error creating WAL for node %d: %v\n", me, err)
		panic(err)
	}
	rf.wal = wal

	// Recover state from WAL
	snapshot := rf.readPersist()
	rf.lastResetTime = time.Now()

	if len(snapshot) > 0 {
		go func() {
			rf.applyCh <- ApplyMsg{
				SnapshotValid: true,
				Snapshot:      snapshot,
				SnapshotTerm:  rf.lastIncludedTerm,
				SnapshotIndex: rf.lastIncludedIndex,
			}
		}()
	}

	go rf.ticker()
	go rf.applier()
	go rf.replicator()
	return rf
}

// getLogEntry returns the log entry at the given absolute index.
//* This expects the index to be present in the current log ( >= LastIncludedIndex ).
func (rf *Raft) getLogEntry(index int) LogEntry {
	if index == rf.lastIncludedIndex {
		// If the index is equal to the last included index, return a dummy entry with that index & term
		return LogEntry{Index: rf.lastIncludedIndex, Term: rf.lastIncludedTerm}
	}
	offset := index - rf.lastIncludedIndex - 1
	// If the offset is negative, it means the index is not in the current log (i.e. it's in the snapshot)
	if offset < 0 {
		return LogEntry{}
	}
	return rf.log[offset]
}

func (rf *Raft) replicator() {
	for {
		select {
		case <-rf.triggerCh:
			// Instead of sending immediately, we sleep for 5ms.
			// This allows multiple 'Start()' calls to pile up entries in rf.log.
			time.Sleep(5 * time.Millisecond)

			rf.mu.Lock()
			rf.persist()
			rf.mu.Unlock()
			// Now we send ONE RPC containing ALL the new entries
			rf.sendHeartBeats()
		}
	}
}

func (rf *Raft) ticker() {
	for {
		//sleep for a short duration of time
		time.Sleep(100 * time.Millisecond)
		rf.reportMetrics()

		rf.mu.Lock()
		currentState := rf.state
		lastReset := rf.lastResetTime
		rf.mu.Unlock()

		if currentState == Leader {
			rf.sendHeartBeats()
		} else {
			// a randomized timeout after which election starts
			electionTimeout := time.Duration(800+rand.Intn(200)) * time.Millisecond
			if time.Since(lastReset) > electionTimeout {
				rf.startElection()
			}
		}
	}
}
func (rf *Raft) startElection() {
	rf.mu.Lock()
	rf.state = Candidate
	rf.currentTerm++
	rf.votedFor = rf.me
	rf.lastResetTime = time.Now()
	rf.persistState()

	term := rf.currentTerm
	lastLogIndex := len(rf.log) - 1
	lastLogTerm := rf.log[lastLogIndex].Term
	rf.mu.Unlock()

	votesReceived := 1 // Vote for self
	votesRequired := len(rf.peers)/2 + 1

	for i := range rf.peers {
		go func(peerIndex int) {
			args := RequestVoteArgs{
				Term:         term,
				CandidateId:  rf.me,
				LastLogTerm:  lastLogTerm,
				LastLogIndex: lastLogIndex,
			}
			reply := RequestVoteReply{}

			if rf.sendRequestVote(peerIndex, &args, &reply) {
				rf.mu.Lock()
				defer rf.mu.Unlock()

				// Discard old replies
				if rf.state != Candidate || rf.currentTerm != term {
					return
				}

				if reply.Term > rf.currentTerm {
					rf.currentTerm = reply.Term
					rf.state = Follower
					rf.votedFor = -1
					rf.leaderId = -1
					rf.persistState()
					return
				}

				if reply.VoteGranted {
					votesReceived++
					if votesReceived == votesRequired {
						rf.state = Leader
						rf.leaderId = rf.me
						for p := range rf.peers {
							rf.nextIndex[p] = rf.getLastLogIndex() + 1
							rf.matchIndex[p] = 0
						}
						go rf.sendHeartBeats()
					}
				}
			}
		}(i)
	}
}

func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	if rf.peers[server] == nil {
		return false
	}
	pbArgs := &pb.RequestVoteRequest{
		Term:         int32(args.Term),
		CandidateId:  int32(args.CandidateId),
		LastLogIndex: int32(args.LastLogIndex),
		LastLogTerm:  int32(args.LastLogTerm),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*100)
	defer cancel()

	pbReply, err := rf.peers[server].RequestVote(ctx, pbArgs)
	if err != nil {
		return false
	}
	reply.Term = int(pbReply.Term)
	reply.VoteGranted = pbReply.VoteGranted
	return true
}

func (rf *Raft) reportMetrics() {
	id := fmt.Sprintf("%d", rf.me)

	metrics.TermGauge.WithLabelValues(id).Set(float64(rf.currentTerm))
	metrics.CommitIndexGauge.WithLabelValues(id).Set(float64(rf.commitIndex))
	metrics.StateGauge.WithLabelValues(id).Set(float64(rf.state))
	metrics.LeaderGauge.WithLabelValues(id).Set(float64(rf.leaderId))
}
