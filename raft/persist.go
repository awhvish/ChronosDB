package raft

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"
)

type SnapshotFile struct {
	LastIncludedIndex int
	LastIncludedTerm  int
	Data              []byte
}

// persist saves Raft log entries to WAL
func (rf *Raft) persist() {
	if rf.wal == nil {
		return
	}

	// Only persist if we have log entries
	if len(rf.log) > 0 {
		// Convert only new entries (WAL.AppendEntries handles deduplication)
		walEntries := make([]WALEntry, 0, len(rf.log))
		for _, entry := range rf.log {
			walEntries = append(walEntries, WALEntry{
				RecordType: RecordTypeLog,
				Index:      uint32(entry.Index),
				Term:       uint32(entry.Term),
				Command:    entry.Command,
			})
		}

		// WAL handles skipping already-persisted entries internally
		rf.wal.AppendEntries(walEntries, 0)
	}
}

// Snapshot is called by the Service (KVStore) to discard old logs
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if index <= rf.lastIncludedIndex {
		return
	}

	// 1. Truncate Log

	// We need to find the term of the entry at 'index'.
	entry := rf.getLogEntry(index)
	newLastIncludedTerm := entry.Term

	// Calculate offset to truncate
	offset := index - rf.lastIncludedIndex

	if offset < len(rf.log) {
		rf.log = rf.log[offset:]
	} else {
		// This means we are trying to access an index beyond what is available (or, case 2)
		// Eg. for Case 2:
		// index = 5, lastIncludedIndex = 3, len(log) >= 2 (bcoz. index need not be the last possible log)
		// offset = 2, len(log) = 2 (So, in this case everything in the log is saved in snapshot, so the new log is empty)
		// Reset to empty log starting at index
		rf.log = make([]LogEntry, 1)
		rf.log[0] = LogEntry{Term: newLastIncludedTerm, Index: index}
	}

	// Clear command for the sentinel entry to save memory
	rf.log[0].Command = nil
	rf.log[0].Index = index
	rf.log[0].Term = newLastIncludedTerm

	rf.lastIncludedIndex = index
	rf.lastIncludedTerm = newLastIncludedTerm

	// 2. Persist Snapshot & HardState
	rf.saveSnapshot(index, newLastIncludedTerm, snapshot)
}

func (rf *Raft) saveSnapshot(index, term int, snapshot []byte) {
	filename := fmt.Sprintf("snapshot_%d.gob", rf.me)

	// Wrap snapshot metadata for persistent storage
	snap := SnapshotFile{
		LastIncludedIndex: index,
		LastIncludedTerm:  term,
		Data:              snapshot,
	}

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(snap); err != nil {
		fmt.Printf("Error encoding snapshot: %v\n", err)
		return
	}

	// Write snapshot to file
	err := os.WriteFile(filename, buf.Bytes(), 0644)
	if err != nil {
		fmt.Printf("Error saving snapshot: %v\n", err)
		return
	}

	// Truncate WAL
	// We must pass current Commit/Vote/Term to ensure HardState relies in new WAL
	err = rf.wal.TruncateWAL(uint32(index), uint32(rf.currentTerm), uint32(rf.votedFor), uint32(rf.commitIndex))
	if err != nil {
		fmt.Printf("Error truncating WAL: %v\n", err)
	}
}

// persistState saves Raft hard state (term, vote, commit) to WAL: only called when these values actually change
func (rf *Raft) persistState() {
	if rf.wal == nil {
		return
	}

	rf.wal.PersistHardState(
		uint32(rf.currentTerm),
		uint32(rf.votedFor),
		uint32(rf.commitIndex),
	)
}

// readPersist restores Raft state from the WAL
func (rf *Raft) readPersist() []byte {
	if rf.wal == nil {
		fmt.Printf("WAL not initialized for node %d\n", rf.me)
		return nil
	}

	var snapshot []byte

	// 1. Recover Snapshot first
	snapshotFile := fmt.Sprintf("snapshot_%d.gob", rf.me)
	snapshotData, err := os.ReadFile(snapshotFile)
	if err == nil && len(snapshotData) > 0 {
		buf := bytes.NewBuffer(snapshotData)
		d := gob.NewDecoder(buf)
		var snap SnapshotFile
		if err := d.Decode(&snap); err == nil {
			rf.lastIncludedIndex = snap.LastIncludedIndex
			rf.lastIncludedTerm = snap.LastIncludedTerm
			snapshot = snap.Data
			fmt.Printf("Node %d recovered snapshot: Index=%d, Term=%d\n", rf.me, rf.lastIncludedIndex, rf.lastIncludedTerm)
		} else {
			fmt.Printf("Node %d failed to decode snapshot: %v\n", rf.me, err)
		}
	}

	walEntries, hardState, err := rf.wal.RecoverEntries()
	if err != nil {
		fmt.Printf("raft readPersist WAL recovery err node %d: %s\n", rf.me, err)
	}

	// Reconstruct log
	rf.log = make([]LogEntry, 0)
	rf.log = append(rf.log, LogEntry{Term: rf.lastIncludedTerm, Index: rf.lastIncludedIndex})

	if len(walEntries) > 0 {
		// In case there are WAL Entries, append them to the log
		for _, walEntry := range walEntries {
			if walEntry.RecordType == RecordTypeLog {
				if int(walEntry.Index) > rf.lastIncludedIndex {
					rf.log = append(rf.log, LogEntry{
						Index:   int(walEntry.Index),
						Term:    int(walEntry.Term),
						Command: walEntry.Command,
					})
				}
			}
		}
	}

	// Restore hard state if available
	if hardState.Term > 0 {
		rf.currentTerm = int(hardState.Term)
		rf.votedFor = int(hardState.Vote)
		rf.commitIndex = int(hardState.Commit)
		fmt.Printf("Node %d recovered hard state: Term=%d, Vote=%d, Commit=%d\n",
			rf.me, rf.currentTerm, rf.votedFor, rf.commitIndex)
	}
	return snapshot
}
