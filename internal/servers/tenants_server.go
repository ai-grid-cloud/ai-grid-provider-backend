package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	aigridv1 "github.com/osac-project/ai-grid-provider-backend/gen/ai_grid/v1"
	"github.com/osac-project/ai-grid-provider-backend/internal/keycloak"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// RFC 1123 DNS label: 1–63 chars, lowercase alphanumeric, hyphens allowed inside.
var tenantNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// organizationClient is the Keycloak Organizations surface used by Tenants.
// Tests inject fakes; production wires *keycloak.Client.
type organizationClient interface {
	ListOrganizations(ctx context.Context) ([]keycloak.Organization, error)
	GetOrganization(ctx context.Context, id string) (keycloak.Organization, error)
	CreateOrganization(ctx context.Context, org keycloak.Organization) (keycloak.Organization, error)
	UpdateOrganization(ctx context.Context, org keycloak.Organization) (keycloak.Organization, error)
	DeleteOrganization(ctx context.Context, id string) error
}

// TenantsServerBuilder configures a TenantsServer.
type TenantsServerBuilder struct {
	logger  *slog.Logger
	tenants organizationClient
}

// TenantsServer implements ai_grid.v1.Tenants.
type TenantsServer struct {
	aigridv1.UnimplementedTenantsServer
	logger  *slog.Logger
	tenants organizationClient
}

var _ aigridv1.TenantsServer = (*TenantsServer)(nil)

// NewTenantsServer creates a builder for TenantsServer.
func NewTenantsServer() *TenantsServerBuilder {
	return &TenantsServerBuilder{}
}

// SetLogger sets the logger. Optional.
func (b *TenantsServerBuilder) SetLogger(logger *slog.Logger) *TenantsServerBuilder {
	b.logger = logger
	return b
}

// SetOrganizationClient sets the Keycloak organization client. Mandatory.
func (b *TenantsServerBuilder) SetOrganizationClient(client organizationClient) *TenantsServerBuilder {
	b.tenants = client
	return b
}

// SetOrganizationLister sets the Keycloak organization client. Mandatory.
// Deprecated: use SetOrganizationClient.
func (b *TenantsServerBuilder) SetOrganizationLister(client organizationClient) *TenantsServerBuilder {
	return b.SetOrganizationClient(client)
}

// Build creates the TenantsServer.
func (b *TenantsServerBuilder) Build() (*TenantsServer, error) {
	if b.tenants == nil {
		return nil, errors.New("keycloak tenant client is mandatory")
	}
	logger := b.logger
	if logger == nil {
		logger = slog.Default()
	}
	return &TenantsServer{logger: logger, tenants: b.tenants}, nil
}

// List returns Keycloak Organizations from the configured realm.
func (s *TenantsServer) List(ctx context.Context, req *aigridv1.TenantsListRequest) (*aigridv1.TenantsListResponse, error) {
	if s.tenants == nil {
		return nil, fmt.Errorf("keycloak tenant client is not configured")
	}
	organizations, err := s.tenants.ListOrganizations(ctx)
	if err != nil {
		return nil, mapTenantKeycloakError("list", "", err)
	}
	items := make([]*aigridv1.Tenant, 0, len(organizations))
	for _, organization := range organizations {
		items = append(items, tenantFromOrganization(organization))
	}
	page, total := applyListWindow(items, req.Offset, req.Limit)
	return &aigridv1.TenantsListResponse{
		Size:  int32(len(page)),
		Total: total,
		Items: page,
	}, nil
}

// Get returns a single tenant by Keycloak Organization id.
func (s *TenantsServer) Get(ctx context.Context, req *aigridv1.TenantsGetRequest) (*aigridv1.TenantsGetResponse, error) {
	id := req.GetId()
	if err := validateTenantID(id); err != nil {
		return nil, err
	}
	organization, err := s.tenants.GetOrganization(ctx, id)
	if err != nil {
		return nil, mapTenantKeycloakError("get", id, err)
	}
	return &aigridv1.TenantsGetResponse{Object: tenantFromOrganization(organization)}, nil
}

// Create creates a Keycloak Organization for the tenant.
// metadata.name is required (RFC 1123). Keycloak assigns the organization id.
func (s *TenantsServer) Create(ctx context.Context, req *aigridv1.TenantsCreateRequest) (*aigridv1.TenantsCreateResponse, error) {
	object := req.GetObject()
	if object == nil {
		return nil, status.Error(codes.InvalidArgument, "object is required")
	}
	name := tenantName(object)
	if err := validateTenantName(name); err != nil {
		return nil, err
	}

	org := keycloak.Organization{
		Name:    name,
		Alias:   name,
		Domains: domainsFromTenantSpec(object.GetSpec()),
	}
	created, err := s.tenants.CreateOrganization(ctx, org)
	if err != nil {
		return nil, mapTenantKeycloakError("create", name, err)
	}
	return &aigridv1.TenantsCreateResponse{Object: tenantFromOrganization(created)}, nil
}

// Update patches tenant name and/or domains according to update_mask.
// An empty mask updates all mutable fields (name + domains).
func (s *TenantsServer) Update(ctx context.Context, req *aigridv1.TenantsUpdateRequest) (*aigridv1.TenantsUpdateResponse, error) {
	object := req.GetObject()
	if object == nil {
		return nil, status.Error(codes.InvalidArgument, "object is required")
	}
	id := object.GetId()
	if err := validateTenantID(id); err != nil {
		return nil, err
	}

	current, err := s.tenants.GetOrganization(ctx, id)
	if err != nil {
		return nil, mapTenantKeycloakError("get", id, err)
	}

	updateName, updateDomains := tenantUpdatePaths(req.GetUpdateMask())
	next := current
	if updateName {
		name := tenantName(object)
		if name == "" {
			name = current.Name
		}
		if err := validateTenantName(name); err != nil {
			return nil, err
		}
		next.Name = name
		next.Alias = name
	}
	if updateDomains {
		if object.GetSpec() == nil {
			next.Domains = nil
		} else {
			next.Domains = domainsFromTenantSpec(object.GetSpec())
		}
	}

	updated, err := s.tenants.UpdateOrganization(ctx, next)
	if err != nil {
		return nil, mapTenantKeycloakError("update", id, err)
	}
	return &aigridv1.TenantsUpdateResponse{Object: tenantFromOrganization(updated)}, nil
}

// Delete deletes a Keycloak Organization by id.
func (s *TenantsServer) Delete(ctx context.Context, req *aigridv1.TenantsDeleteRequest) (*aigridv1.TenantsDeleteResponse, error) {
	id := req.GetId()
	if err := validateTenantID(id); err != nil {
		return nil, err
	}
	if err := s.tenants.DeleteOrganization(ctx, id); err != nil {
		return nil, mapTenantKeycloakError("delete", id, err)
	}
	return &aigridv1.TenantsDeleteResponse{}, nil
}

func tenantFromOrganization(organization keycloak.Organization) *aigridv1.Tenant {
	return &aigridv1.Tenant{
		Id: organization.ID,
		Metadata: &aigridv1.Metadata{
			Name: organization.Name,
		},
		Spec: &aigridv1.TenantSpec{
			Domains: organization.DomainNames(),
		},
		Status: &aigridv1.TenantStatus{},
	}
}

func tenantName(object *aigridv1.Tenant) string {
	if object.GetMetadata() != nil && object.GetMetadata().GetName() != "" {
		return object.GetMetadata().GetName()
	}
	return object.GetId()
}

func domainsFromTenantSpec(spec *aigridv1.TenantSpec) []keycloak.OrganizationDomain {
	if spec == nil {
		return nil
	}
	domains := make([]keycloak.OrganizationDomain, 0, len(spec.GetDomains()))
	for _, domain := range spec.GetDomains() {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			continue
		}
		domains = append(domains, keycloak.OrganizationDomain{Name: domain})
	}
	return domains
}

func tenantUpdatePaths(mask *fieldmaskpb.FieldMask) (name, domains bool) {
	if mask == nil || len(mask.GetPaths()) == 0 {
		return true, true
	}
	for _, path := range mask.GetPaths() {
		p := strings.TrimPrefix(path, "object.")
		switch {
		case p == "metadata.name" || p == "metadata" || strings.HasPrefix(p, "metadata."):
			name = true
		case p == "spec.domains" || p == "spec" || strings.HasPrefix(p, "spec."):
			domains = true
		}
	}
	return name, domains
}

func validateTenantID(id string) error {
	if strings.TrimSpace(id) == "" {
		return status.Error(codes.InvalidArgument, "id is required")
	}
	return nil
}

func validateTenantName(name string) error {
	if name == "" {
		return status.Error(codes.InvalidArgument, "metadata.name is required")
	}
	if !tenantNameRE.MatchString(name) {
		return status.Errorf(codes.InvalidArgument, "metadata.name %q is not a valid RFC 1123 DNS label", name)
	}
	return nil
}

func mapTenantKeycloakError(op, id string, err error) error {
	switch {
	case keycloak.IsNotFound(err):
		if id == "" {
			return status.Errorf(codes.NotFound, "tenant not found")
		}
		return status.Errorf(codes.NotFound, "tenant %q not found", id)
	case keycloak.IsConflict(err):
		if id == "" {
			return status.Errorf(codes.AlreadyExists, "tenant already exists")
		}
		return status.Errorf(codes.AlreadyExists, "tenant %q already exists", id)
	case keycloak.IsBadRequest(err):
		return status.Errorf(codes.InvalidArgument, "%s tenant %q: %v", op, id, err)
	case keycloak.IsForbidden(err):
		return status.Errorf(codes.PermissionDenied, "%s tenant %q: %v", op, id, err)
	default:
		if id == "" {
			return fmt.Errorf("%s tenants: %w", op, err)
		}
		return fmt.Errorf("%s tenant %q: %w", op, id, err)
	}
}
