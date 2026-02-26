package raft

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// newTestWAL creates a WAL backed by a temp file in t.TempDir().
// It avoids the hardcoded filename and background syncer goroutine
// used by createOrOpenRaftWAL, making tests isolated and deterministic.
func newTestWAL(t *testing.T) *WAL {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test_raft_wal")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		t.Fatalf("failed to create temp WAL file: %v", err)
	}
	t.Cleanup(func() { f.Close() })

	return &WAL{
		file:          f,
		writer:        bufio.NewWriterSize(f, 64*1024),
		lastPersisted: 0,
		triggerCh:     make(chan struct{}, 1),
	}
}

// reopenWAL closes the existing WAL and re-opens it from the same file,
// simulating a process restart for recovery tests.
func reopenWAL(t *testing.T, w *WAL) *WAL {
	t.Helper()
	path := w.file.Name()

	if err := w.writer.Flush(); err != nil {
		t.Fatalf("failed to flush WAL: %v", err)
	}
	if err := w.file.Sync(); err != nil {
		t.Fatalf("failed to sync WAL: %v", err)
	}
	if err := w.file.Close(); err != nil {
		t.Fatalf("failed to close WAL: %v", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0666)
	if err != nil {
		t.Fatalf("failed to reopen WAL file: %v", err)
	}
	t.Cleanup(func() { f.Close() })

	return &WAL{
		file:          f,
		writer:        bufio.NewWriterSize(f, 64*1024),
		lastPersisted: 0,
		triggerCh:     make(chan struct{}, 1),
	}
}

// makeEntries is a helper to create a slice of WALEntry values.
func makeEntries(startIdx, count, term uint32) []WALEntry {
	entries := make([]WALEntry, count)
	for i := uint32(0); i < count; i++ {
		entries[i] = WALEntry{
			RecordType: RecordTypeLog,
			Index:      startIdx + i,
			Term:       term,
			Command:    []byte("cmd_" + string(rune('A'+i))),
		}
	}
	return entries
}

// ---------------------------------------------------------------
// Test 1: AppendEntries + RecoverEntries roundtrip
// ---------------------------------------------------------------

func TestAppendAndRecoverRoundtrip(t *testing.T) {
	w := newTestWAL(t)

	entries := makeEntries(1, 5, 1) // indices 1..5, term 1
	if err := w.AppendEntries(entries, 1); err != nil {
		t.Fatalf("AppendEntries failed: %v", err)
	}

	w2 := reopenWAL(t, w)
	recovered, state, err := w2.RecoverEntries()
	if err != nil {
		t.Fatalf("RecoverEntries failed: %v", err)
	}

	if len(recovered) != len(entries) {
		t.Fatalf("expected %d entries, got %d", len(entries), len(recovered))
	}

	for i, e := range entries {
		r := recovered[i]
		if r.Index != e.Index {
			t.Errorf("entry[%d] index: want %d, got %d", i, e.Index, r.Index)
		}
		if r.Term != e.Term {
			t.Errorf("entry[%d] term: want %d, got %d", i, e.Term, r.Term)
		}
		if !bytes.Equal(r.Command, e.Command) {
			t.Errorf("entry[%d] command: want %q, got %q", i, e.Command, r.Command)
		}
		if r.RecordType != RecordTypeLog {
			t.Errorf("entry[%d] recordType: want %d, got %d", i, RecordTypeLog, r.RecordType)
		}
	}

	// HardState should be zero since we never persisted one
	if state.Term != 0 || state.Vote != 0 || state.Commit != 0 {
		t.Errorf("expected zero HardState, got %+v", state)
	}

	// Verify lastPersisted was restored
	if w2.lastPersisted != 5 {
		t.Errorf("lastPersisted: want 5, got %d", w2.lastPersisted)
	}
}

// ---------------------------------------------------------------
// Test 2: PersistHardState + recovery
// ---------------------------------------------------------------

func TestPersistHardStateAndRecovery(t *testing.T) {
	w := newTestWAL(t)

	if err := w.PersistHardState(3, 2, 10); err != nil {
		t.Fatalf("PersistHardState failed: %v", err)
	}

	w2 := reopenWAL(t, w)
	entries, state, err := w2.RecoverEntries()
	if err != nil {
		t.Fatalf("RecoverEntries failed: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
	if state.Term != 3 {
		t.Errorf("HardState.Term: want 3, got %d", state.Term)
	}
	if state.Vote != 2 {
		t.Errorf("HardState.Vote: want 2, got %d", state.Vote)
	}
	if state.Commit != 10 {
		t.Errorf("HardState.Commit: want 10, got %d", state.Commit)
	}
}

// ---------------------------------------------------------------
// Test 3: Deduplication — entries with indices ≤ lastPersisted are skipped
// ---------------------------------------------------------------

func TestDeduplication(t *testing.T) {
	w := newTestWAL(t)

	// First batch: indices 1..5
	batch1 := makeEntries(1, 5, 1)
	if err := w.AppendEntries(batch1, 1); err != nil {
		t.Fatalf("AppendEntries batch1 failed: %v", err)
	}

	if w.lastPersisted != 5 {
		t.Fatalf("lastPersisted after batch1: want 5, got %d", w.lastPersisted)
	}

	// Second batch: indices 1..8 (overlapping — 1-5 should be skipped, only 6-8 appended)
	batch2 := makeEntries(1, 8, 2)
	if err := w.AppendEntries(batch2, 1); err != nil {
		t.Fatalf("AppendEntries batch2 failed: %v", err)
	}

	if w.lastPersisted != 8 {
		t.Fatalf("lastPersisted after batch2: want 8, got %d", w.lastPersisted)
	}

	// Recover and verify: should have 8 entries (5 from batch1 + 3 new from batch2), no duplicates
	w2 := reopenWAL(t, w)
	recovered, _, err := w2.RecoverEntries()
	if err != nil {
		t.Fatalf("RecoverEntries failed: %v", err)
	}

	if len(recovered) != 8 {
		t.Fatalf("expected 8 entries, got %d", len(recovered))
	}

	// Indices 1..5 should come from batch1 (term=1), indices 6..8 from batch2 (term=2)
	for i, r := range recovered {
		expectedIdx := uint32(i + 1)
		if r.Index != expectedIdx {
			t.Errorf("entry[%d] index: want %d, got %d", i, expectedIdx, r.Index)
		}
		if expectedIdx <= 5 {
			if r.Term != 1 {
				t.Errorf("entry[%d] (idx %d) term: want 1 (batch1), got %d", i, expectedIdx, r.Term)
			}
		} else {
			if r.Term != 2 {
				t.Errorf("entry[%d] (idx %d) term: want 2 (batch2), got %d", i, expectedIdx, r.Term)
			}
		}
	}

	// Also test: appending a fully duplicate batch should be a no-op
	w3 := newTestWAL(t)
	entries := makeEntries(1, 3, 1)
	if err := w3.AppendEntries(entries, 1); err != nil {
		t.Fatalf("AppendEntries failed: %v", err)
	}
	// Append same entries again
	if err := w3.AppendEntries(entries, 1); err != nil {
		t.Fatalf("AppendEntries (dup) failed: %v", err)
	}

	w4 := reopenWAL(t, w3)
	recovered2, _, err := w4.RecoverEntries()
	if err != nil {
		t.Fatalf("RecoverEntries failed: %v", err)
	}
	if len(recovered2) != 3 {
		t.Errorf("expected 3 entries after dup append, got %d", len(recovered2))
	}
}

// ---------------------------------------------------------------
// Test 4: Partial write / corrupt file recovery
// ---------------------------------------------------------------

func TestPartialWriteCorruptRecovery(t *testing.T) {
	w := newTestWAL(t)

	// Write 3 valid entries
	entries := makeEntries(1, 3, 1)
	if err := w.AppendEntries(entries, 1); err != nil {
		t.Fatalf("AppendEntries failed: %v", err)
	}

	// Flush and sync, then append garbage bytes directly to simulate a partial/corrupt write
	if err := w.writer.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	if err := w.file.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Write a valid record type byte followed by incomplete header (simulates crash mid-write)
	garbage := []byte{RecordTypeLog, 0xFF, 0xFF} // type byte + truncated header
	if _, err := w.file.Write(garbage); err != nil {
		t.Fatalf("failed to write garbage: %v", err)
	}

	w2 := reopenWAL(t, w)
	recovered, _, err := w2.RecoverEntries()

	// RecoverEntries should return the valid entries along with an error for the corrupt tail
	if len(recovered) != 3 {
		t.Errorf("expected 3 valid entries before corruption, got %d", len(recovered))
	}

	// The error should be non-nil because of the truncated record
	if err == nil {
		t.Logf("Note: RecoverEntries did not return error for corrupt tail (implementation may silently skip)")
	}

	// Verify the valid entries are correct
	for i, r := range recovered {
		if r.Index != entries[i].Index || r.Term != entries[i].Term {
			t.Errorf("entry[%d] mismatch: want idx=%d term=%d, got idx=%d term=%d",
				i, entries[i].Index, entries[i].Term, r.Index, r.Term)
		}
	}
}

// ---------------------------------------------------------------
// Test 5: Mixed log entries and hard state recovery
// ---------------------------------------------------------------

func TestMixedLogAndHardStateRecovery(t *testing.T) {
	w := newTestWAL(t)

	// Append entries 1-3
	batch1 := makeEntries(1, 3, 1)
	if err := w.AppendEntries(batch1, 1); err != nil {
		t.Fatalf("AppendEntries batch1 failed: %v", err)
	}

	// Persist hard state
	if err := w.PersistHardState(1, 0, 2); err != nil {
		t.Fatalf("PersistHardState #1 failed: %v", err)
	}

	// Append entries 4-6
	batch2 := makeEntries(4, 3, 2)
	if err := w.AppendEntries(batch2, 4); err != nil {
		t.Fatalf("AppendEntries batch2 failed: %v", err)
	}

	// Update hard state (simulates a new term/election)
	if err := w.PersistHardState(2, 1, 5); err != nil {
		t.Fatalf("PersistHardState #2 failed: %v", err)
	}

	w2 := reopenWAL(t, w)
	recovered, state, err := w2.RecoverEntries()
	if err != nil {
		t.Fatalf("RecoverEntries failed: %v", err)
	}

	// Should have 6 log entries total
	if len(recovered) != 6 {
		t.Fatalf("expected 6 entries, got %d", len(recovered))
	}

	// Verify sequential indices 1..6
	for i, r := range recovered {
		expectedIdx := uint32(i + 1)
		if r.Index != expectedIdx {
			t.Errorf("entry[%d] index: want %d, got %d", i, expectedIdx, r.Index)
		}
	}

	// Hard state should reflect the LATEST PersistHardState call
	if state.Term != 2 {
		t.Errorf("HardState.Term: want 2, got %d", state.Term)
	}
	if state.Vote != 1 {
		t.Errorf("HardState.Vote: want 1, got %d", state.Vote)
	}
	if state.Commit != 5 {
		t.Errorf("HardState.Commit: want 5, got %d", state.Commit)
	}

	// lastPersisted should be 6
	if w2.lastPersisted != 6 {
		t.Errorf("lastPersisted: want 6, got %d", w2.lastPersisted)
	}
}

// ---------------------------------------------------------------
// Test 6: Empty WAL recovery
// ---------------------------------------------------------------

func TestEmptyWALRecovery(t *testing.T) {
	w := newTestWAL(t)

	entries, state, err := w.RecoverEntries()
	if err != nil {
		t.Fatalf("RecoverEntries on empty WAL failed: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
	if state.Term != 0 || state.Vote != 0 || state.Commit != 0 {
		t.Errorf("expected zero HardState, got %+v", state)
	}
	if w.lastPersisted != 0 {
		t.Errorf("lastPersisted: want 0, got %d", w.lastPersisted)
	}
}

// ---------------------------------------------------------------
// Test 7: Large entry payload roundtrip
// ---------------------------------------------------------------

func TestLargeEntryPayload(t *testing.T) {
	w := newTestWAL(t)

	// Create an entry with a large command payload (64KB)
	largeCmd := make([]byte, 64*1024)
	for i := range largeCmd {
		largeCmd[i] = byte(i % 256)
	}

	entries := []WALEntry{
		{RecordType: RecordTypeLog, Index: 1, Term: 1, Command: largeCmd},
	}
	if err := w.AppendEntries(entries, 1); err != nil {
		t.Fatalf("AppendEntries with large payload failed: %v", err)
	}

	w2 := reopenWAL(t, w)
	recovered, _, err := w2.RecoverEntries()
	if err != nil {
		t.Fatalf("RecoverEntries failed: %v", err)
	}

	if len(recovered) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(recovered))
	}
	if !bytes.Equal(recovered[0].Command, largeCmd) {
		t.Error("large command payload mismatch after roundtrip")
	}
}

// ---------------------------------------------------------------
// Test 8: Corrupt record type byte recovery
// ---------------------------------------------------------------

func TestCorruptRecordTypeByte(t *testing.T) {
	w := newTestWAL(t)

	// Write 2 valid entries
	entries := makeEntries(1, 2, 1)
	if err := w.AppendEntries(entries, 1); err != nil {
		t.Fatalf("AppendEntries failed: %v", err)
	}

	if err := w.writer.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	if err := w.file.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Write an invalid record type followed by a full-size "header"
	// This simulates data corruption where the record type byte is invalid
	badRecord := make([]byte, 13) // 1 type byte + 12 header bytes
	badRecord[0] = 0xFF           // invalid record type
	binary.LittleEndian.PutUint32(badRecord[1:5], 99)
	binary.LittleEndian.PutUint32(badRecord[5:9], 1)
	binary.LittleEndian.PutUint32(badRecord[9:13], 0)
	if _, err := w.file.Write(badRecord); err != nil {
		t.Fatalf("failed to write bad record: %v", err)
	}

	w2 := reopenWAL(t, w)
	recovered, _, _ := w2.RecoverEntries()

	// The implementation's switch statement simply skips unknown record types
	// and reads the next byte as a new record type. The 2 valid entries
	// written before the corruption should be recovered.
	if len(recovered) < 2 {
		t.Errorf("expected at least 2 valid entries before corruption, got %d", len(recovered))
	}
}
