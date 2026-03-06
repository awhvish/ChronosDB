package main

import (
	"KV-Store/kv"
	"KV-Store/pkg/arena"
	"KV-Store/pkg/wal"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// createTestStore creates a minimal kv.Store for testing
func createTestStore(t *testing.T) *kv.Store {
	walDir := t.TempDir()
	sstDir := t.TempDir()

	currentWal, err := wal.OpenWAL(walDir, 0)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}

	store := &kv.Store{
		ActiveMap: &kv.MemTable{
			Index: make(map[string]int),
			Arena: arena.NewArena(10 * 1024 * 1024),
			Wal:   currentWal,
		},
		WalDir:    walDir,
		SstDir:    sstDir,
		FlushChan: make(chan struct{}, 1),
		Me:        0,
	}

	// Close WAL when test completes
	t.Cleanup(func() {
		if store.ActiveMap != nil && store.ActiveMap.Wal != nil {
			store.ActiveMap.Wal.Close()
		}
	})

	return store
}

// putKeyValue is a helper to add data to the store for testing
func putKeyValue(store *kv.Store, key, value string) error {
	offset, err := store.ActiveMap.Arena.Put(key, value, false)
	if err != nil {
		return err
	}
	store.ActiveMap.Index[key] = offset
	return nil
}

// TestHandleGet tests the GET handler
func TestHandleGet(t *testing.T) {
	tests := []struct {
		name           string
		key            string
		setupData      map[string]string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "successful get",
			key:            "testkey",
			setupData:      map[string]string{"testkey": "testvalue"},
			expectedStatus: http.StatusOK,
			expectedBody:   "testvalue",
		},
		{
			name:           "key not found",
			key:            "nonexistent",
			setupData:      map[string]string{},
			expectedStatus: http.StatusNotFound,
			expectedBody:   "Key not found\n",
		},
		{
			name:           "empty key",
			key:            "",
			setupData:      map[string]string{"": "emptykey"},
			expectedStatus: http.StatusOK,
			expectedBody:   "emptykey",
		},
		{
			name:           "special characters in key",
			key:            "key@test",
			setupData:      map[string]string{"key@test": "special"},
			expectedStatus: http.StatusOK,
			expectedBody:   "special",
		},
		{
			name:           "unicode characters",
			key:            "键",
			setupData:      map[string]string{"键": "值"},
			expectedStatus: http.StatusOK,
			expectedBody:   "值",
		},
		{
			name:           "newline in value",
			key:            "key",
			setupData:      map[string]string{"key": "line1\nline2"},
			expectedStatus: http.StatusOK,
			expectedBody:   "line1\nline2",
		},
		{
			name:           "tab in value",
			key:            "key",
			setupData:      map[string]string{"key": "val\tue"},
			expectedStatus: http.StatusOK,
			expectedBody:   "val\tue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := createTestStore(t)

			// Setup test data
			for k, v := range tt.setupData {
				if err := putKeyValue(store, k, v); err != nil {
					t.Fatalf("Failed to setup test data: %v", err)
				}
			}

			// Build URL with proper encoding
			reqURL := "/get?key=" + url.QueryEscape(tt.key)
			req := httptest.NewRequest(http.MethodGet, reqURL, nil)
			w := httptest.NewRecorder()

			handler := handleGet(store)
			handler(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if w.Body.String() != tt.expectedBody {
				t.Errorf("expected body %q, got %q", tt.expectedBody, w.Body.String())
			}
		})
	}
}

// TestHandleGetEdgeCases tests additional edge cases for GET
func TestHandleGetEdgeCases(t *testing.T) {
	t.Run("multiple query parameters", func(t *testing.T) {
		store := createTestStore(t)
		putKeyValue(store, "key1", "value1")

		req := httptest.NewRequest(http.MethodGet, "/get?key=key1&extra=param", nil)
		w := httptest.NewRecorder()

		handler := handleGet(store)
		handler(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}

		if w.Body.String() != "value1" {
			t.Errorf("expected body %q, got %q", "value1", w.Body.String())
		}
	})

	t.Run("URL encoded key with spaces", func(t *testing.T) {
		store := createTestStore(t)
		putKeyValue(store, "key with spaces", "value")

		req := httptest.NewRequest(http.MethodGet, "/get?key="+url.QueryEscape("key with spaces"), nil)
		w := httptest.NewRecorder()

		handler := handleGet(store)
		handler(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
	})

	t.Run("very long key", func(t *testing.T) {
		store := createTestStore(t)
		longKey := ""
		for i := 0; i < 100; i++ {
			longKey += "a"
		}
		putKeyValue(store, longKey, "longvalue")

		req := httptest.NewRequest(http.MethodGet, "/get?key="+url.QueryEscape(longKey), nil)
		w := httptest.NewRecorder()

		handler := handleGet(store)
		handler(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}
	})
}

// TestHandleDelete tests the DELETE handler
func TestHandleDelete(t *testing.T) {
	t.Run("empty key returns bad request", func(t *testing.T) {
		store := createTestStore(t)

		req := httptest.NewRequest(http.MethodDelete, "/delete?key=", nil)
		w := httptest.NewRecorder()

		handler := handleDelete(store, 0, "http://localhost:800%d")
		handler(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}

		expectedBody := "key is required\n"
		if w.Body.String() != expectedBody {
			t.Errorf("expected body %q, got %q", expectedBody, w.Body.String())
		}
	})

	t.Run("missing key parameter", func(t *testing.T) {
		store := createTestStore(t)

		req := httptest.NewRequest(http.MethodDelete, "/delete", nil)
		w := httptest.NewRecorder()

		handler := handleDelete(store, 0, "http://localhost:800%d")
		handler(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}
	})
}

// TestStatusCodeConsistency verifies consistent status codes across handlers
func TestStatusCodeConsistency(t *testing.T) {
	t.Run("404 for not found scenarios", func(t *testing.T) {
		store := createTestStore(t)

		// GET non-existent key
		req := httptest.NewRequest(http.MethodGet, "/get?key=missing", nil)
		w := httptest.NewRecorder()
		handleGet(store)(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("GET: expected 404, got %d", w.Code)
		}
	})

	t.Run("400 for bad requests", func(t *testing.T) {
		store := createTestStore(t)

		// DELETE with empty key
		req := httptest.NewRequest(http.MethodDelete, "/delete?key=", nil)
		w := httptest.NewRecorder()
		handleDelete(store, 0, "http://localhost:800%d")(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("DELETE: expected 400, got %d", w.Code)
		}
	})

	t.Run("200 for successful GET", func(t *testing.T) {
		store := createTestStore(t)
		putKeyValue(store, "key", "value")

		// GET success
		req := httptest.NewRequest(http.MethodGet, "/get?key=key", nil)
		w := httptest.NewRecorder()
		handleGet(store)(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("GET: expected 200, got %d", w.Code)
		}
	})
}

// TestHandlerResponseBodies verifies response body content
func TestHandlerResponseBodies(t *testing.T) {
	t.Run("GET returns exact value", func(t *testing.T) {
		store := createTestStore(t)
		putKeyValue(store, "key", "exact value")

		req := httptest.NewRequest(http.MethodGet, "/get?key=key", nil)
		w := httptest.NewRecorder()
		handleGet(store)(w, req)

		if w.Body.String() != "exact value" {
			t.Errorf("expected %q, got %q", "exact value", w.Body.String())
		}
	})

	t.Run("GET not found returns error message", func(t *testing.T) {
		store := createTestStore(t)

		req := httptest.NewRequest(http.MethodGet, "/get?key=missing", nil)
		w := httptest.NewRecorder()
		handleGet(store)(w, req)

		if w.Body.String() != "Key not found\n" {
			t.Errorf("expected %q, got %q", "Key not found\n", w.Body.String())
		}
	})

	t.Run("DELETE empty key returns error message", func(t *testing.T) {
		store := createTestStore(t)

		req := httptest.NewRequest(http.MethodDelete, "/delete?key=", nil)
		w := httptest.NewRecorder()
		handleDelete(store, 0, "http://localhost:800%d")(w, req)

		if w.Body.String() != "key is required\n" {
			t.Errorf("expected %q, got %q", "key is required\n", w.Body.String())
		}
	})
}

// TestSpecialCharacterHandling tests how handlers deal with special characters
func TestSpecialCharacterHandling(t *testing.T) {
	specialChars := []struct {
		name  string
		key   string
		value string
	}{
		{"ampersand", "keytest", "valuetest"},
		{"equals", "keytest2", "valuetest2"},
		{"hash", "keytest3", "valuetest3"},
		{"percent", "keytest4", "valuetest4"},
		{"slash", "keytest5", "valuetest5"},
		{"quotes", "keytest6", "valuetest6"},
	}

	for _, tc := range specialChars {
		t.Run(tc.name, func(t *testing.T) {
			store := createTestStore(t)
			putKeyValue(store, tc.key, tc.value)

			req := httptest.NewRequest(http.MethodGet, "/get?key="+url.QueryEscape(tc.key), nil)
			w := httptest.NewRecorder()
			handleGet(store)(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", w.Code)
			}

			if w.Body.String() != tc.value {
				t.Errorf("expected %q, got %q", tc.value, w.Body.String())
			}
		})
	}
}

// TestEmptyAndWhitespaceValues tests handling of empty and whitespace values
func TestEmptyAndWhitespaceValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"empty value", "key", ""},
		{"space value", "key2", " "},
		{"tab value", "key3", "\t"},
		{"newline value", "key4", "\n"},
		{"multiple spaces", "key5", "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := createTestStore(t)
			putKeyValue(store, tt.key, tt.value)

			req := httptest.NewRequest(http.MethodGet, "/get?key="+url.QueryEscape(tt.key), nil)
			w := httptest.NewRecorder()
			handleGet(store)(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", w.Code)
			}

			if w.Body.String() != tt.value {
				t.Errorf("expected %q, got %q", tt.value, w.Body.String())
			}
		})
	}
}

// TestConcurrentRequests tests that handlers can handle concurrent requests
func TestConcurrentRequests(t *testing.T) {
	store := createTestStore(t)

	// Setup some data
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key%d", i)
		value := fmt.Sprintf("value%d", i)
		putKeyValue(store, key, value)
	}

	// Make concurrent GET requests
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(index int) {
			key := fmt.Sprintf("key%d", index)
			req := httptest.NewRequest(http.MethodGet, "/get?key="+url.QueryEscape(key), nil)
			w := httptest.NewRecorder()
			handleGet(store)(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("concurrent request %d: expected status 200, got %d", index, w.Code)
			}
			done <- true
		}(i)
	}

	// Wait for all requests to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestLargeValues tests handling of large values
func TestLargeValues(t *testing.T) {
	store := createTestStore(t)

	// Test with 1KB value
	largeValue := ""
	for i := 0; i < 1024; i++ {
		largeValue += "a"
	}

	putKeyValue(store, "largekey", largeValue)

	req := httptest.NewRequest(http.MethodGet, "/get?key=largekey", nil)
	w := httptest.NewRecorder()
	handleGet(store)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if len(w.Body.String()) != len(largeValue) {
		t.Errorf("expected value length %d, got %d", len(largeValue), len(w.Body.String()))
	}
}
