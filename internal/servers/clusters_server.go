package servers

import (
	"context"
	"errors"
	"log/slog"

	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// managedClusterFacade is the ManagedClusters surface used by the Clusters presentation layer.
type managedClusterFacade interface {
	List(context.Context, *aigridv1.ManagedClustersListRequest) (*aigridv1.ManagedClustersListResponse, error)
	Get(context.Context, *aigridv1.ManagedClustersGetRequest) (*aigridv1.ManagedClustersGetResponse, error)
	Update(context.Context, *aigridv1.ManagedClustersUpdateRequest) (*aigridv1.ManagedClustersUpdateResponse, error)
}

// ClustersServerBuilder configures a ClustersServer.
type ClustersServerBuilder struct {
	logger   *slog.Logger
	clusters managedClusterFacade
}

// ClustersServer implements ai_grid.v1.Clusters as a PoC-shaped facade over ManagedClusters.
type ClustersServer struct {
	aigridv1.UnimplementedClustersServer
	logger   *slog.Logger
	clusters managedClusterFacade
}

var _ aigridv1.ClustersServer = (*ClustersServer)(nil)

// NewClustersServer creates a builder for ClustersServer.
func NewClustersServer() *ClustersServerBuilder {
	return &ClustersServerBuilder{}
}

// SetLogger sets the logger. Optional.
func (b *ClustersServerBuilder) SetLogger(logger *slog.Logger) *ClustersServerBuilder {
	b.logger = logger
	return b
}

// SetManagedClusters sets the ManagedClusters backend. Mandatory.
func (b *ClustersServerBuilder) SetManagedClusters(clusters managedClusterFacade) *ClustersServerBuilder {
	b.clusters = clusters
	return b
}

// Build creates the ClustersServer.
func (b *ClustersServerBuilder) Build() (*ClustersServer, error) {
	if b.clusters == nil {
		return nil, errors.New("ManagedClusters facade is mandatory")
	}
	logger := b.logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ClustersServer{logger: logger, clusters: b.clusters}, nil
}

// List returns GridCluster presentation objects for the console Clusters page.
func (s *ClustersServer) List(ctx context.Context, req *aigridv1.ClustersListRequest) (*aigridv1.ClustersListResponse, error) {
	listed, err := s.clusters.List(ctx, &aigridv1.ManagedClustersListRequest{
		Offset: req.Offset,
		Limit:  req.Limit,
	})
	if err != nil {
		return nil, err
	}

	items := make([]*aigridv1.GridCluster, 0, len(listed.GetItems()))
	for _, mc := range listed.GetItems() {
		items = append(items, gridClusterFromManagedCluster(mc))
	}
	return &aigridv1.ClustersListResponse{
		Size:  int32(len(items)),
		Total: listed.GetTotal(),
		Items: items,
	}, nil
}

// Update enrolls a cluster into AI Grid (label ai-grid/enabled=true).
// Only { "ai_grid_enabled": true } is accepted (PoC enroll semantics).
func (s *ClustersServer) Update(ctx context.Context, req *aigridv1.ClustersUpdateRequest) (*aigridv1.ClustersUpdateResponse, error) {
	name := req.GetName()
	if err := validateManagedClusterID(name); err != nil {
		return nil, err
	}
	if req.AiGridEnabled == nil || !req.GetAiGridEnabled() {
		return nil, status.Error(codes.InvalidArgument, "only ai_grid_enabled: true is supported")
	}

	updated, err := s.clusters.Update(ctx, &aigridv1.ManagedClustersUpdateRequest{
		Object: &aigridv1.ManagedCluster{
			Id: name,
			Metadata: &aigridv1.Metadata{
				Labels: enrollLabelPatch(),
			},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
	})
	if err != nil {
		return nil, err
	}

	object := gridClusterFromManagedCluster(updated.GetObject())
	if !object.GetAiGridEnabled() {
		return nil, status.Error(codes.Unavailable, "hub did not confirm AI Grid membership")
	}
	return &aigridv1.ClustersUpdateResponse{Object: object}, nil
}
