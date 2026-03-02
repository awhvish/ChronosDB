package raft

import (
	pb "KV-Store/proto"
	"context"
	"fmt"
	"time"
)

type AppendEntriesArgs struct {
	Term         int //leader's term
	LeaderId     int
	PrevLogIndex int        //index of log entry preceding new ones
	PrevLogTerm  int        // term of prev log index
	Entries      []LogEntry //log entries to store, empty for heartbeats
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term          int
	Success       bool
	ConflictIndex int
	ConflictTerm  int
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// Term Check
	if args.Term < rf.currentTerm {
		reply.Term = rf.currentTerm
		reply.Success = false
		return
	}

	// Heartbeat & State Management
	rf.currentTerm = args.Term
	rf.state = Follower
	rf.votedFor = -1
	rf.leaderId = args.LeaderId
	rf.lastResetTime = time.Now()
	rf.persistState()
	reply.Term = rf.currentTerm

	// Log Consistency Check
	lastLogIndex := rf.getLastLogIndex()

	// Case A: Follower log is shorter than Leader's PrevLogIndex
	if args.PrevLogIndex > lastLogIndex {
		reply.Success = false
		reply.ConflictTerm = -1
		reply.ConflictIndex = rf.getLastLogIndex() + 1
		return
	}

	// Case B: Term mismatch at PrevLogIndex
	// Use getLogEntry to safely access the log, but remember PrevLogIndex might be in the snapshot.
	// If PrevLogIndex < rf.lastIncludedIndex, we should theoretically have returned false,
	// but let's handle the strict check:
	if args.PrevLogIndex < rf.lastIncludedIndex {
		reply.Success = false
		reply.ConflictIndex = rf.lastIncludedIndex + 1
		reply.ConflictTerm = -1 // No term info for compacted logs (bcoz. we don't know the term for the snapshotted logs)
		return
	}

	myTermAtPrevIndex := rf.getLogEntry(args.PrevLogIndex).Term
	if myTermAtPrevIndex != args.PrevLogTerm {
		reply.Success = false
		reply.ConflictTerm = myTermAtPrevIndex

		// Find the VERY FIRST index of this conflicting term (scan backwards)
		// This allows jumping over an entire term of bad data
		// We only scan down to LastIncludedIndex + 1, bcoz. we don't have anything before that available
		for i := args.PrevLogIndex; i > rf.lastIncludedIndex; i-- {
			if rf.getLogEntry(i).Term == reply.ConflictTerm {
				reply.ConflictIndex = i
			} else {
				break
			}
		}
		return
	}

	//  Append Entries (Safe Merge)
	insertIndex := args.PrevLogIndex + 1
	for i, entry := range args.Entries {
		index := insertIndex + i
		if index < len(rf.log) {
			// If we find a conflict, truncate the rest and start fresh
			if rf.log[index].Term != entry.Term {
				rf.log = rf.log[:index]
				rf.log = append(rf.log, entry)
			}
		} else {
			// No conflict, just append
			rf.log = append(rf.log, entry)
		}
	}

	// Persist if we changed anything
	if len(args.Entries) > 0 {
		rf.persist()
	}

	// Update Commit Index
	if args.LeaderCommit > rf.commitIndex {
		// min(LeaderCommit, Index of last new entry)
		lastNewIndex := args.PrevLogIndex + len(args.Entries)
		if args.LeaderCommit < lastNewIndex {
			rf.commitIndex = args.LeaderCommit
		} else {
			rf.commitIndex = lastNewIndex
		}
		// Signal applier
		select {
		case rf.commitCh <- struct{}{}:
		default:
		}
	}

	reply.Success = true
}

func (rf *Raft) sendHeartBeats() {
	rf.mu.Lock()
	if rf.state != Leader {
		rf.mu.Unlock()
		return
	}
	term := rf.currentTerm
	rf.mu.Unlock()

	for i := range rf.peers {
		if i == rf.me {
			continue
		}

		go func(server int) {
			rf.mu.Lock()
			if rf.state != Leader {
				rf.mu.Unlock()
				return
			}

			// Check if we need to send a snapshot
			if rf.nextIndex[server] <= rf.lastIncludedIndex {
				rf.mu.Unlock()
				rf.sendInstallSnapshot(server)
				return
			}

			prevLogIndex := rf.nextIndex[server] - 1
			if prevLogIndex < 0 {
				prevLogIndex = 0
			}

			entries := make([]LogEntry, 0)
			lastIdx := len(rf.log)
			nextIdx := rf.nextIndex[server]

			// Prevent OOM by sending max 100 entries at a time
			if lastIdx > nextIdx {
				endIdx := nextIdx + 100
				if endIdx > lastIdx {
					endIdx = lastIdx
				}
				entries = append(entries, rf.log[nextIdx:endIdx]...)
			}
			if len(entries) > 1 {
				fmt.Printf("Batched RPC sent with %d entries to Node %d!\n", len(entries), i)
			}
			args := AppendEntriesArgs{
				Term:         term,
				LeaderId:     rf.me,
				PrevLogIndex: prevLogIndex,
				// Guard against empty log access
				PrevLogTerm:  0,
				Entries:      entries,
				LeaderCommit: rf.commitIndex,
			}
			if prevLogIndex < len(rf.log) {
				args.PrevLogTerm = rf.log[prevLogIndex].Term
			}

			rf.mu.Unlock()

			var reply AppendEntriesReply
			if rf.sendAppendEntries(server, &args, &reply) {
				rf.mu.Lock()
				defer rf.mu.Unlock()

				if reply.Term > rf.currentTerm {
					rf.currentTerm = reply.Term
					rf.state = Follower
					rf.votedFor = -1
					rf.leaderId = -1
					return
				}

				if reply.Success {
					// Success: Advance indices
					newMatchIndex := args.PrevLogIndex + len(args.Entries)
					if newMatchIndex > rf.matchIndex[server] {
						rf.matchIndex[server] = newMatchIndex
						rf.nextIndex[server] = rf.matchIndex[server] + 1
					}

					// Check if we can commit
					for N := len(rf.log) - 1; N > rf.commitIndex; N-- {
						count := 1
						for peer := range rf.peers {
							if peer != rf.me && rf.matchIndex[peer] >= N {
								count++
							}
						}
						if count > len(rf.peers)/2 && rf.log[N].Term == rf.currentTerm {
							rf.commitIndex = N
							// Signal applier
							select {
							case rf.commitCh <- struct{}{}:
							default:
							}
							break
						}
					}
				} else {
					// SMART BACKTRACKING ---

					if reply.ConflictTerm == -1 {
						// Follower log is shorter than ours. Jump to their end.
						rf.nextIndex[server] = reply.ConflictIndex
					} else {
						// Search for ConflictTerm in our log
						lastIndexOfTerm := -1
						for i := len(rf.log) - 1; i >= 0; i-- {
							if rf.log[i].Term == reply.ConflictTerm {
								lastIndexOfTerm = i
								break
							}
						}

						if lastIndexOfTerm != -1 {
							// We have that term! Start just after it.
							rf.nextIndex[server] = lastIndexOfTerm + 1
						} else {
							// We don't have that term. Skip it entirely.
							rf.nextIndex[server] = reply.ConflictIndex
						}
					}

					// Sanity check
					if rf.nextIndex[server] < 1 {
						rf.nextIndex[server] = 1
					}
				}
			}
		}(i)
	}
}
func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	// 1. Convert to Proto
	pbEntries := make([]*pb.LogEntry, len(args.Entries))
	for i, v := range args.Entries {
		pbEntries[i] = &pb.LogEntry{
			Index:   int32(v.Index),
			Term:    int32(v.Term),
			Command: v.Command,
		}
	}

	pbArgs := &pb.AppendEntriesRequest{
		Term:         int32(args.Term),
		LeaderId:     int32(args.LeaderId),
		PrevLogIndex: int32(args.PrevLogIndex),
		PrevLogTerm:  int32(args.PrevLogTerm),
		Entries:      pbEntries,
		LeaderCommit: int32(args.LeaderCommit),
	}

	// 2. Call gRPC
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*500)
	defer cancel()

	pbReply, err := rf.peers[server].AppendEntries(ctx, pbArgs)
	if err != nil {
		return false
	}

	// 3. Unpack Response
	reply.Term = int(pbReply.Term)
	reply.Success = pbReply.Success
	reply.ConflictTerm = int(pbReply.ConflictTerm)
	reply.ConflictIndex = int(pbReply.ConflictIndex)

	return true
}
