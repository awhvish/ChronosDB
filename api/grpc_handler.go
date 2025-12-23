package api

import pb "KV-Store/proto"

type GrpcServer struct {
	pb.UnimplementedKVServiceServer
}
