package keycloak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

const identityProviderOrgIDMapperName = "AI Grid org id"

// IdentityProvider is a realm-level identity provider instance.
type IdentityProvider struct {
	Alias       string            `json:"alias"`
	DisplayName string            `json:"displayName,omitempty"`
	ProviderID  string            `json:"providerId"`
	Enabled     bool              `json:"enabled"`
	Config      map[string]string `json:"config"`
}

// IdentityProviderMapper is an IdP mapper representation.
type IdentityProviderMapper struct {
	ID                     string            `json:"id,omitempty"`
	Name                   string            `json:"name"`
	IdentityProviderAlias  string            `json:"identityProviderAlias"`
	IdentityProviderMapper string            `json:"identityProviderMapper"`
	Config                 map[string]string `json:"config"`
}

// OrganizationIdentityProviderLink configures an org↔IdP association.
type OrganizationIdentityProviderLink struct {
	AutoMembership bool   `json:"autoMembership"`
	MembershipType string `json:"membershipType"`
}

// CreateIdentityProvider creates a realm-level identity provider.
func (c *Client) CreateIdentityProvider(ctx context.Context, idp IdentityProvider) error {
	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(idp)
	if err != nil {
		return fmt.Errorf("marshal identity provider: %w", err)
	}
	return c.doJSON(ctx, http.MethodPost, c.idpInstancesURL(), token, payload, nil, "create identity provider")
}

// DeleteIdentityProvider deletes a realm-level identity provider by alias.
// A 404 is treated as success.
func (c *Client) DeleteIdentityProvider(ctx context.Context, alias string) error {
	if alias == "" {
		return fmt.Errorf("identity provider alias is required")
	}
	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	err = c.doJSON(ctx, http.MethodDelete, c.idpInstanceURL(alias), token, nil, nil, "delete identity provider")
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}

// LinkOrganizationIdentityProvider associates a realm IdP with an organization.
func (c *Client) LinkOrganizationIdentityProvider(ctx context.Context, orgID, alias string) error {
	if orgID == "" || alias == "" {
		return fmt.Errorf("organization id and identity provider alias are required")
	}
	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(alias)
	if err != nil {
		return fmt.Errorf("marshal identity provider alias: %w", err)
	}
	return c.doJSON(ctx, http.MethodPost, c.orgIdpURL(orgID), token, payload, nil, "link organization identity provider")
}

// ConfigureOrganizationIdentityProvider sets MANAGED auto-membership on the org IdP link.
func (c *Client) ConfigureOrganizationIdentityProvider(ctx context.Context, orgID, alias string) error {
	if orgID == "" || alias == "" {
		return fmt.Errorf("organization id and identity provider alias are required")
	}
	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(OrganizationIdentityProviderLink{
		AutoMembership: true,
		MembershipType: "MANAGED",
	})
	if err != nil {
		return fmt.Errorf("marshal organization identity provider link: %w", err)
	}
	return c.doJSON(ctx, http.MethodPut, c.orgIdpAliasURL(orgID, alias), token, payload, nil, "configure organization identity provider")
}

// UpsertIdentityProviderMapper creates or updates the AI Grid org id hardcoded-attribute mapper.
func (c *Client) UpsertIdentityProviderMapper(ctx context.Context, alias, organizationID string) error {
	if alias == "" || organizationID == "" {
		return fmt.Errorf("identity provider alias and organization id are required")
	}
	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	var mappers []IdentityProviderMapper
	if err := c.doJSON(ctx, http.MethodGet, c.idpMappersURL(alias), token, nil, &mappers, "list identity provider mappers"); err != nil {
		return err
	}
	mapper := IdentityProviderMapper{
		Name:                   identityProviderOrgIDMapperName,
		IdentityProviderAlias:  alias,
		IdentityProviderMapper: "hardcoded-attribute-idp-mapper",
		Config: map[string]string{
			"attribute":       "maas_org_id",
			"attribute.value": organizationID,
			"syncMode":        "FORCE",
		},
	}
	for _, existing := range mappers {
		if existing.Name != identityProviderOrgIDMapperName {
			continue
		}
		mapper.ID = existing.ID
		payload, err := json.Marshal(mapper)
		if err != nil {
			return fmt.Errorf("marshal identity provider mapper: %w", err)
		}
		return c.doJSON(ctx, http.MethodPut, c.idpMapperURL(alias, existing.ID), token, payload, nil, "update identity provider mapper")
	}
	payload, err := json.Marshal(mapper)
	if err != nil {
		return fmt.Errorf("marshal identity provider mapper: %w", err)
	}
	return c.doJSON(ctx, http.MethodPost, c.idpMappersURL(alias), token, payload, nil, "create identity provider mapper")
}

func (c *Client) idpInstancesURL() string {
	return c.baseURL + "/admin/realms/" + url.PathEscape(c.realm) + "/identity-provider/instances"
}

func (c *Client) idpInstanceURL(alias string) string {
	return c.idpInstancesURL() + "/" + url.PathEscape(alias)
}

func (c *Client) idpMappersURL(alias string) string {
	return c.idpInstanceURL(alias) + "/mappers"
}

func (c *Client) idpMapperURL(alias, id string) string {
	return c.idpMappersURL(alias) + "/" + url.PathEscape(id)
}

func (c *Client) orgIdpURL(orgID string) string {
	return c.orgURL(orgID) + "/identity-providers"
}

func (c *Client) orgIdpAliasURL(orgID, alias string) string {
	return c.orgIdpURL(orgID) + "/" + url.PathEscape(alias)
}
