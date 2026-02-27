package main

import (
	"fmt"
	"net/http"

	"KV-Store/kv"
)

func handleGet(store *kv.Store, nodeID int, peerTemplate string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		consistency := r.URL.Query().Get("consistency")

		// If consistency=strong is requested, redirect to leader
		if consistency == "strong" {
			leaderID := store.Raft.GetLeader()
			if leaderID == -1 {
				http.Error(w, "Leader not found", http.StatusNotFound)
				return
			}
			if leaderID != nodeID {
				leaderURL := fmt.Sprintf(peerTemplate, leaderID)
				targetURL := fmt.Sprintf("%s/get?key=%s&consistency=strong", leaderURL, key)
				forwardToLeader(w, r, targetURL)
				return
			}
		}

		val, found := store.Get(key)
		if !found {
			http.Error(w, "Key not found", http.StatusNotFound)
			return
		}
		w.Write([]byte(val))
	}
}

func handlePut(store *kv.Store, nodeID int, peerTemplate string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		val := r.URL.Query().Get("val")

		err := store.Put(key, val, false)
		if err != nil {
			if err.Error() == "not leader" {
				leaderID := store.Raft.GetLeader()
				if leaderID == -1 {
					http.Error(w, "Leader not found", http.StatusNotFound)
					return
				}
				if leaderID == nodeID {
					http.Error(w, "Cluster in leadership transition", http.StatusServiceUnavailable)
					return
				}
				leaderURL := fmt.Sprintf(peerTemplate, leaderID)
				targetURL := fmt.Sprintf("%s/put?key=%s&val=%s", leaderURL, key, val)
				forwardToLeader(w, r, targetURL)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte("Success"))
	}
}

func handleDelete(store *kv.Store, nodeID int, peerTemplate string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")

		if key == "" {
			http.Error(w, "key is required", http.StatusBadRequest)
			return
		}
		err := store.Put(key, "", true)
		if err != nil {
			if err.Error() == "not leader" {
				leaderID := store.Raft.GetLeader()
				if leaderID == -1 {
					http.Error(w, "Leader not found", http.StatusNotFound)
					return
				}
				if leaderID == nodeID {
					http.Error(w, "Cluster in leadership transition", http.StatusServiceUnavailable)
					return
				}
				leaderURL := fmt.Sprintf(peerTemplate, leaderID)
				targetURL := fmt.Sprintf("%s/delete?key=%s", leaderURL, key)
				forwardToLeader(w, r, targetURL)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
