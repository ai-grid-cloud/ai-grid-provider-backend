package client

import (
	"context"
	"errors"
	"fmt"

	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// Builder configures a client connection to ai-grid-provider-backend.
type Builder struct {
	address   string
	plaintext bool
}

// NewBuilder creates a client builder.
func NewBuilder() *Builder { return &Builder{} }

// SetAddress sets the server address, for example localhost:8080.
func (b *Builder) SetAddress(address string) *Builder {
	b.address = address
	return b
}

// SetPlaintext enables a connection without TLS, intended for local development.
func (b *Builder) SetPlaintext(plaintext bool) *Builder {
	b.plaintext = plaintext
	return b
}

// Build creates a connected client.
func (b *Builder) Build(ctx context.Context) (*Client, error) {
	if b.address == "" {
		return nil, errors.New("server address is mandatory")
	}
	if !b.plaintext {
		return nil, errors.New("TLS is not configured; call SetPlaintext(true) for a local server")
	}

	connection, err := grpc.DialContext(ctx, b.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("connect to ai-grid-provider-backend: %w", err)
	}
	return &Client{
		connection:      connection,
		aiGrid:          aigridv1.NewAiGridClient(connection),
		managedClusters: aigridv1.NewManagedClustersClient(connection),
		clusters:        aigridv1.NewClustersClient(connection),
		tenants:         aigridv1.NewTenantsClient(connection),
	}, nil
}

// Client calls the ai-grid-provider-backend gRPC API.
type Client struct {
	connection      *grpc.ClientConn
	aiGrid          aigridv1.AiGridClient
	managedClusters aigridv1.ManagedClustersClient
	clusters        aigridv1.ClustersClient
	tenants         aigridv1.TenantsClient
}

// Ping invokes ai_grid.v1.AiGrid/Ping.
func (c *Client) Ping(ctx context.Context, name string) (string, error) {
	response, err := c.aiGrid.Ping(ctx, &aigridv1.AiGridPingRequest{Name: name})
	if err != nil {
		return "", err
	}
	return response.GetMessage(), nil
}

// ListManagedClusters returns ManagedCluster objects from the hub.
func (c *Client) ListManagedClusters(ctx context.Context) ([]*aigridv1.ManagedCluster, error) {
	response, err := c.managedClusters.List(ctx, &aigridv1.ManagedClustersListRequest{})
	if err != nil {
		return nil, err
	}
	return response.GetItems(), nil
}

// GetManagedCluster returns a single ManagedCluster by id.
func (c *Client) GetManagedCluster(ctx context.Context, id string) (*aigridv1.ManagedCluster, error) {
	response, err := c.managedClusters.Get(ctx, &aigridv1.ManagedClustersGetRequest{Id: id})
	if err != nil {
		return nil, err
	}
	return response.GetObject(), nil
}

// CreateManagedCluster creates a hub-side ManagedCluster CR.
func (c *Client) CreateManagedCluster(ctx context.Context, object *aigridv1.ManagedCluster) (*aigridv1.ManagedCluster, error) {
	response, err := c.managedClusters.Create(ctx, &aigridv1.ManagedClustersCreateRequest{Object: object})
	if err != nil {
		return nil, err
	}
	return response.GetObject(), nil
}

// UpdateManagedCluster patches an existing ManagedCluster. A nil mask updates all mutable fields.
func (c *Client) UpdateManagedCluster(ctx context.Context, object *aigridv1.ManagedCluster, mask *fieldmaskpb.FieldMask) (*aigridv1.ManagedCluster, error) {
	response, err := c.managedClusters.Update(ctx, &aigridv1.ManagedClustersUpdateRequest{
		Object:     object,
		UpdateMask: mask,
	})
	if err != nil {
		return nil, err
	}
	return response.GetObject(), nil
}

// DeleteManagedCluster deletes a ManagedCluster by id.
func (c *Client) DeleteManagedCluster(ctx context.Context, id string) error {
	_, err := c.managedClusters.Delete(ctx, &aigridv1.ManagedClustersDeleteRequest{Id: id})
	return err
}

// ListClusters returns PoC-shaped GridCluster presentation objects.
func (c *Client) ListClusters(ctx context.Context) ([]*aigridv1.GridCluster, error) {
	response, err := c.clusters.List(ctx, &aigridv1.ClustersListRequest{})
	if err != nil {
		return nil, err
	}
	return response.GetItems(), nil
}

// EnrollCluster sets ai-grid/enabled=true on the named hub ManagedCluster.
func (c *Client) EnrollCluster(ctx context.Context, name string) (*aigridv1.GridCluster, error) {
	enabled := true
	response, err := c.clusters.Update(ctx, &aigridv1.ClustersUpdateRequest{
		Name:          name,
		AiGridEnabled: &enabled,
	})
	if err != nil {
		return nil, err
	}
	return response.GetObject(), nil
}

// ListTenants returns Keycloak Organizations exposed as ai-grid tenants.
func (c *Client) ListTenants(ctx context.Context) ([]*aigridv1.Tenant, error) {
	response, err := c.tenants.List(ctx, &aigridv1.TenantsListRequest{})
	if err != nil {
		return nil, err
	}
	return response.GetItems(), nil
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error { return c.connection.Close() }
