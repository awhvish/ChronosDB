package raft

import (
	pb "KV-Store/proto"
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"os"
	"time"
)

type InstallSnapshotArgs struct {
	Term              int
	LeaderId          int
	LastIncludedIndex int
	LastIncludedTerm  int
	Offset            int64
	Data              []byte
	Done              bool
}

type InstallSnapshotReply struct {
	Term int
}

// sendInstallSnapshot is the method called by the Leader to send a snapshot to a lagging follower.
// It handles reading the snapshot file, chunking it, and sending it via RPC.
func (rf *Raft) sendInstallSnapshot(server int) {
	rf.mu.Lock()
	if rf.state != Leader {
		rf.mu.Unlock()
		return
	}

	term := rf.currentTerm
	leaderID := rf.me
	lastIncludedIndex := rf.lastIncludedIndex
	lastIncludedTerm := rf.lastIncludedTerm
	rf.mu.Unlock()

	// Read the snapshot file
	filename := fmt.Sprintf("snapshot_%d.gob", rf.me)
	file, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Error opening snapshot file: %v\n", err)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return
	}
	fileSize := stat.Size()
	offset := int64(0)
	chunkSize := int64(32 * 1024)

	for offset < fileSize {
		// Calculate chunk size
		remaining := fileSize - offset
		if remaining < chunkSize {
			chunkSize = remaining
		}

		data := make([]byte, chunkSize)
		n, err := file.ReadAt(data, offset)
		if err != nil && n == 0 {
			break
		}

		args := InstallSnapshotArgs{
			Term:              term,
			LeaderId:          leaderID,
			LastIncludedIndex: lastIncludedIndex,
			LastIncludedTerm:  lastIncludedTerm,
			Offset:            offset,
			Data:              data,
			Done:              (offset + int64(n)) >= fileSize,
		}
		reply := InstallSnapshotReply{}

		if rf.sendInstallSnapshotToPeer(server, &args, &reply) {
			rf.mu.Lock()
			if reply.Term > rf.currentTerm {
				rf.currentTerm = reply.Term
				rf.state = Follower
				rf.votedFor = -1
				rf.persistState()
				rf.mu.Unlock()
				return
			}
			rf.mu.Unlock()

			// Update offset
			offset += int64(n)
		} else {
			// If this heartbeat is unable to send a message we can't keep retrying in this goroutine again & again
			// For now exit, next heartbeat will retry
			return
		}
	}

	// If finished successfully, update indices
	rf.mu.Lock()
	if rf.state == Leader {
		rf.nextIndex[server] = lastIncludedIndex + 1
		rf.matchIndex[server] = lastIncludedIndex
	}
	rf.mu.Unlock()
}

// sendInstallSnapshotToPeer wrapper to make the actual gRPC call
// Returns true if RPC succeeded, false otherwise.
func (rf *Raft) sendInstallSnapshotToPeer(server int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) bool {
	// Convert Go struct to Proto struct
	pbArgs := &pb.InstallSnapshotRequest{
		Term:              int32(args.Term),
		LeaderId:          int32(args.LeaderId),
		LastIncludedIndex: int32(args.LastIncludedIndex),
		LastIncludedTerm:  int32(args.LastIncludedTerm),
		Offset:            args.Offset,
		Data:              args.Data,
		Done:              args.Done,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Make RPC call
	if rf.peers[server] == nil {
		return false
	}
	pbReply, err := rf.peers[server].InstallSnapshot(ctx, pbArgs)
	if err != nil {
		fmt.Printf("InstallSnapshot RPC error to node %d: %v\n", server, err)
		return false
	}

	// Store the term in reply
	reply.Term = int(pbReply.Term)
	return true
}

// InstallSnapshot is the RPC Handler on the Follower side.
// It accepts chunks of the snapshot and reconstructs the state.
func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	reply.Term = rf.currentTerm

	// If candidate term is smaller than current term reject immediately
	if args.Term < rf.currentTerm {
		return
	}
	// If we see a newer term, update our state
	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.state = Follower
		rf.votedFor = -1
		rf.persistState()
	}
	rf.lastResetTime = time.Now() // Treat implementation of snapshot as a heartbeat

	// If the snapshot is older than the current log, ignore it
	if args.LastIncludedIndex <= rf.getLastLogIndex() {
		return
	}

	// Write data to a temporary snapshot file at 'args.Offset'.
	// If args.Offset == 0, create/truncate the temp file.

	// Write the snapshot data to a temporary file
	if args.Offset == 0 {
		// Create/truncate the temp file
		var err error
		rf.snapshotFile, err = os.Create(fmt.Sprintf("snapshot_temp_%d.gob", rf.me))
		if err != nil {
			fmt.Printf("Error creating snapshot file: %v\n", err)
			return
		}
	}

	// If we are appending to existing file
	if args.Offset > 0 {
		if rf.snapshotFile == nil {
			// Optimization: Try to reopen if nil (e.g. restart or lost handle), though unsafe if we seek wrong.
			// Ideally we reject if offset > 0 but no file open.
			// return
		}
	}

	// Write chunk
	if rf.snapshotFile != nil {
		_, err := rf.snapshotFile.WriteAt(args.Data, args.Offset)
		if err != nil {
			fmt.Printf("Error writing snapshot chunk: %v\n", err)
			return
		}
	}

	// 4. If args.Done == true:
	if args.Done {
		if rf.snapshotFile != nil {
			rf.snapshotFile.Close()
			rf.snapshotFile = nil
		}

		// Atomically rename temp file to valid snapshot file
		tempFilename := fmt.Sprintf("snapshot_temp_%d.gob", rf.me)
		finalFilename := fmt.Sprintf("snapshot_%d.gob", rf.me)
		err := os.Rename(tempFilename, finalFilename)
		if err != nil {
			fmt.Printf("Error moving temp snapshot file: %v\n", err)
			return
		}

		// Apply the snapshot to Raft state
		// Check for existing log entry compliance
		// If existing log entry has same index and term as snapshot’s last included entry, retain log entries following it
		preserveLog := false
		if args.LastIncludedIndex > rf.lastIncludedIndex {
			// Calculate relative index in current log
			relativeIndex := args.LastIncludedIndex - rf.lastIncludedIndex
			if relativeIndex < len(rf.log) && rf.log[relativeIndex].Term == args.LastIncludedTerm {
				preserveLog = true
				// Retain log entries following LastIncludedIndex
				rf.log = rf.log[relativeIndex:]
				// Safely sanitize the 0-th element which is now the boundary
				rf.log[0].Command = nil
				rf.log[0].Index = args.LastIncludedIndex
				rf.log[0].Term = args.LastIncludedTerm
			}
		}

		if !preserveLog {
			rf.log = make([]LogEntry, 0)                                                                  // Discard log
			rf.log = append(rf.log, LogEntry{Term: args.LastIncludedTerm, Index: args.LastIncludedIndex}) // Sentinel
		}

		rf.lastIncludedIndex = args.LastIncludedIndex
		rf.lastIncludedTerm = args.LastIncludedTerm
		rf.lastApplied = args.LastIncludedIndex
		rf.commitIndex = args.LastIncludedIndex

		rf.persistState()

		// Truncate WAL to match new snapshot
		// We use currentTerm/votedFor/commitIndex from state, but index is the snapshot index
		err = rf.wal.TruncateWAL(uint32(rf.lastIncludedIndex), uint32(rf.currentTerm), uint32(rf.votedFor), uint32(rf.commitIndex))
		if err != nil {
			fmt.Printf("Error truncating WAL after install snapshot: %v\n", err)
		}

		// Send to KV Store
		msg := ApplyMsg{
			SnapshotValid: true,
			Snapshot:      nil,
			SnapshotTerm:  args.LastIncludedTerm,
			SnapshotIndex: args.LastIncludedIndex,
		}

		// Reload snapshot data to send to KV
		snapData, err := os.ReadFile(finalFilename)
		if err == nil {
			buf := bytes.NewBuffer(snapData)
			d := gob.NewDecoder(buf)
			var snap SnapshotFile
			if err := d.Decode(&snap); err == nil {
				msg.Snapshot = snap.Data
			} else {
				fmt.Printf("Error decoding snapshot for KV store: %v\n", err)
				msg.Snapshot = snapData // Fallback
			}
		}

		rf.applyCh <- msg
	}
}
