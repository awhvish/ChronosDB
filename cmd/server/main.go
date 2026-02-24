package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"KV-Store/api"
	"KV-Store/kv"
	pb "KV-Store/proto"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	id := flag.Int("id", 0, "Node ID")
	peerAddrs := flag.String("peers", "", "Comma-separated peer addresses")
	rpcPort := flag.String("port", "5001", "gRPC port")
	httpPort := flag.String("http", "8001", "HTTP port")
	peerTemplate := flag.String("peer-template", "http://kv-%d:8001", "Peer URL template")
	flag.Parse()

	// Initialize Raft clients
	peerList := strings.Split(*peerAddrs, ",")
	raftClients := make([]pb.RaftServiceClient, len(peerList))
	for i, addr := range peerList {
		if i == *id {
			continue
		}
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatalf("Failed to connect to peer %s: %v", addr, err)
		}
		raftClients[i] = pb.NewRaftServiceClient(conn)
	}

	// Initialize store
	store, err := kv.NewKVStore(raftClients, *id)
	if err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}

	// Start gRPC server
	go startGRPCServer(*rpcPort, store)

	// Register HTTP handlers
	http.HandleFunc("/get", httpLogger(withMetrics(handleGet(store, *id, *peerTemplate), "GET", "/get")))
	http.HandleFunc("/put", httpLogger(withMetrics(handlePut(store, *id, *peerTemplate), "PUT", "/put")))
	http.HandleFunc("/delete", httpLogger(withMetrics(handleDelete(store, *id, *peerTemplate), "DELETE", "/delete")))
	http.Handle("/metrics", promhttp.Handler())

	fmt.Printf("HTTP server listening on :%s\n", *httpPort)
	log.Fatal(http.ListenAndServe(":"+*httpPort, nil))
}

func startGRPCServer(port string, store *kv.Store) {
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	server := grpc.NewServer()
	pb.RegisterRaftServiceServer(server, api.NewRaftServer(store.Raft))

	fmt.Printf("gRPC server listening on :%s\n", port)
	if err := server.Serve(lis); err != nil {
		log.Fatalf("gRPC serve failed: %v", err)
	}
}
