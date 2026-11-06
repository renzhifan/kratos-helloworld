package service

import (
	"context"

	pb "logisticsServer/api/encrypt/v1"
)

type EncryptService struct {
	pb.UnimplementedEncryptServer
}

func NewEncryptService() *EncryptService {
	return &EncryptService{}
}

func (s *EncryptService) CreateEncrypt(ctx context.Context, req *pb.CreateEncryptRequest) (*pb.CreateEncryptReply, error) {
	return &pb.CreateEncryptReply{}, nil
}
func (s *EncryptService) GetEncrypt(ctx context.Context, req *pb.GetEncryptRequest) (*pb.GetEncryptReply, error) {
	return &pb.GetEncryptReply{}, nil
}
func (s *EncryptService) BatchGetEncrypt(ctx context.Context, req *pb.BatchGetEncryptRequest) (*pb.BatchGetEncryptReply, error) {
	return &pb.BatchGetEncryptReply{}, nil
}
func (s *EncryptService) Ping(ctx context.Context, req *pb.PingRequest) (*pb.PingReply, error) {
	return &pb.PingReply{}, nil
}
