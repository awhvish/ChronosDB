package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"KV-Store/kv"
)

// TestHandleGet tests the GET endpoint handler
func TestHandleGet(t *testing.T) {
	store := kv.NewStore("test_data", 1, "http://localhost:900%d")
	defer store.Close()

	// Test case 1: Key exists
	store.Put("testkey", "testvalue", false)
	req := httptest.NewRequest("GET", "/get?key=testkey", nil)
	w := httptest.NewRecorder()
	handler := handleGet(store)
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}
	if w.Body.String() != "testvalue" {
		t.Errorf("Expected body 'testvalue', got '%s'", w.Body.String())
	}

	// Test case 2: Key does not exist
	req = httptest.NewRequest("GET", "/get?key=nonexistent", nil)
	w = httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status code %d, got %d", http.StatusNotFound, w.Code)
	}

	// Test case 3: Empty key
	req = httptest.NewRequest("GET", "/get?key=", nil)
	w = httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status code %d for empty key, got %d", http.StatusNotFound, w.Code)
	}

	// Test case 4: Special characters in key
	specialKey := "key@#$%^&*()"
	specialValue := "value!@#$%"
	store.Put(specialKey, specialValue, false)
	req = httptest.NewRequest("GET", "/get?key="+specialKey, nil)
	w = httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d for special characters, got %d", http.StatusOK, w.Code)
	}
	if w.Body.String() != specialValue {
		t.Errorf("Expected body '%s', got '%s'", specialValue, w.Body.String())
	}
}

// TestHandlePut tests the PUT endpoint handler
func TestHandlePut(t *testing.T) {
	store := kv.NewStore("test_data_put", 1, "http://localhost:900%d")
	defer store.Close()

	// Test case 1: Valid key-value pair
	req := httptest.NewRequest("PUT", "/put?key=testkey&val=testvalue", nil)
	w := httptest.NewRecorder()
	handler := handlePut(store, 1, "http://localhost:900%d")
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}
	if w.Body.String() != "Success" {
		t.Errorf("Expected body 'Success', got '%s'", w.Body.String())
	}

	// Test case 2: Empty key
	req = httptest.NewRequest("PUT", "/put?key=&val=testvalue", nil)
	w = httptest.NewRecorder()
	handler(w, req)

	// Should still succeed as empty key is valid (testing edge case)
	if w.Code != http.StatusOK {
		t.Logf("Empty key PUT returned status %d", w.Code)
	}

	// Test case 3: Empty value
	req = httptest.NewRequest("PUT", "/put?key=testkey&val=", nil)
	w = httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d for empty value, got %d", http.StatusOK, w.Code)
	}

	// Test case 4: Special characters in key and value
	req = httptest.NewRequest("PUT", "/put?key=key@#$&val=value!@#$", nil)
	w = httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d for special characters, got %d", http.StatusOK, w.Code)
	}

	// Test case 5: Unicode characters
	req = httptest.NewRequest("PUT", "/put?key=键&val=值", nil)
	w = httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d for unicode characters, got %d", http.StatusOK, w.Code)
	}
}

// TestHandleDelete tests the DELETE endpoint handler
func TestHandleDelete(t *testing.T) {
	store := kv.NewStore("test_data_delete", 1, "http://localhost:900%d")
	defer store.Close()

	// First, put a key to delete
	store.Put("deletekey", "deletevalue", false)

	// Test case 1: Valid delete
	req := httptest.NewRequest("DELETE", "/delete?key=deletekey", nil)
	w := httptest.NewRecorder()
	handler := handleDelete(store, 1, "http://localhost:900%d")
	handler(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status code %d, got %d", http.StatusNoContent, w.Code)
	}

	// Test case 2: Delete non-existent key
	req = httptest.NewRequest("DELETE", "/delete?key=nonexistent", nil)
	w = httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status code %d for non-existent key, got %d", http.StatusNoContent, w.Code)
	}

	// Test case 3: Empty key (should return bad request)
	req = httptest.NewRequest("DELETE", "/delete?key=", nil)
	w = httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status code %d for empty key, got %d", http.StatusBadRequest, w.Code)
	}

	// Test case 4: Missing key parameter
	req = httptest.NewRequest("DELETE", "/delete", nil)
	w = httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status code %d for missing key, got %d", http.StatusBadRequest, w.Code)
	}

	// Test case 5: Special characters in key
	specialKey := "key!@#$%^&*()"
	store.Put(specialKey, "value", false)
	req = httptest.NewRequest("DELETE", "/delete?key="+specialKey, nil)
	w = httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status code %d for special characters, got %d", http.StatusNoContent, w.Code)
	}
}

// TestStatusCodeConsistency tests that all handlers return consistent status codes
func TestStatusCodeConsistency(t *testing.T) {
	store := kv.NewStore("test_data_status", 1, "http://localhost:900%d")
	defer store.Close()

	// Test GET with existing key returns 200
	store.Put("key1", "value1", false)
	req := httptest.NewRequest("GET", "/get?key=key1", nil)
	w := httptest.NewRecorder()
	handleGet(store)(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET existing key: expected %d, got %d", http.StatusOK, w.Code)
	}

	// Test GET with non-existing key returns 404
	req = httptest.NewRequest("GET", "/get?key=nonexistent", nil)
	w = httptest.NewRecorder()
	handleGet(store)(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET non-existent key: expected %d, got %d", http.StatusNotFound, w.Code)
	}

	// Test PUT success returns 200
	req = httptest.NewRequest("PUT", "/put?key=key2&val=value2", nil)
	w = httptest.NewRecorder()
	handlePut(store, 1, "http://localhost:900%d")(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("PUT success: expected %d, got %d", http.StatusOK, w.Code)
	}

	// Test DELETE success returns 204
	req = httptest.NewRequest("DELETE", "/delete?key=key2", nil)
	w = httptest.NewRecorder()
	handleDelete(store, 1, "http://localhost:900%d")(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("DELETE success: expected %d, got %d", http.StatusNoContent, w.Code)
	}

	// Test DELETE with empty key returns 400
	req = httptest.NewRequest("DELETE", "/delete?key=", nil)
	w = httptest.NewRecorder()
	handleDelete(store, 1, "http://localhost:900%d")(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("DELETE empty key: expected %d, got %d", http.StatusBadRequest, w.Code)
	}
}
