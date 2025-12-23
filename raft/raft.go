package raft

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

type State int

const (
	Follower State = iota
	Candidate
	Leader
)

type LogEntry struct {
	index   int
	term    int
	Command []byte
}

type Raft struct {
	mu      sync.Mutex
	peers   []interface{} // RPC clients to talk to other nodes
	me      int           // this peer's index into peers[]
	applyCh chan LogEntry // Channel to send committed data to the KV Store

	//persistent states
	currentTerm int
	votedFor    int
	log         []LogEntry

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

func Make(peers []interface{}, me int, applyCh chan LogEntry) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.me = me
	rf.applyCh = applyCh

	rf.state = Follower
	rf.currentTerm = 0
	rf.votedFor = -1
	rf.log = make([]LogEntry, 0)

	rf.log = append(rf.log, LogEntry{term: 0})

	rf.commitIndex = 0
	rf.lastApplied = 0

	rf.nextIndex = make(map[int]int)
	rf.matchIndex = make(map[int]int)

	rf.lastResetTime = time.Now()

	go rf.ticker()
	return rf
}

func (rf *Raft) ticker() {
	for {
		//sleep for a short duration of time
		time.Sleep(10 * time.Millisecond)

		rf.mu.Lock()
		currentState := rf.state
		lastReset := rf.lastResetTime
		rf.mu.Unlock()

		if currentState == Leader {
			rf.sendHeartBeats()
		} else {
			// a randomized timeout after which election starts
			electionTimeout := time.Duration(300+rand.Intn(300)) * time.Millisecond
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
	term := rf.currentTerm
	rf.mu.Unlock()

	fmt.Printf("Node %d starting election timer for term %d", rf.me, term)
	// TODO: Send Request Vote RPCs
}

func (rf *Raft) sendHeartBeats() {
	// TODO: Send heartbeat as a leader to all peers
}
