package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/manifest/mcpregistry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPMetadata declares tool-call policy. HTTP bearer authentication remains at
// the /mcp mount; this policy also protects in-memory and other SDK transports.
type MCPMetadata struct {
	RequiresUser bool `json:"requires_user"`
}

// MCPInventory describes the tool catalog independently of whether HTTP MCP is
// enabled. The HTTP inventory owns mount availability and bearer policy. This
// function opens no stores, invokes no actions, and starts no protocol sessions.
func MCPInventory() mcpregistry.Inventory[MCPMetadata] {
	return newMCPRegistry(nil).Inventory()
}

func newMCPRegistry(app *core.Core) *mcpregistry.Registry[MCPMetadata] {
	compiler := func(spec mcpregistry.Spec[MCPMetadata]) ([]mcpregistry.Stage, error) {
		if !spec.Metadata.RequiresUser {
			return nil, fmt.Errorf("MCP tools must require an authenticated user")
		}
		return []mcpregistry.Stage{{Name: "require-mcp-user", Prepare: func(ctx context.Context, _ *mcp.CallToolRequest) (context.Context, error) {
			user, err := requireMCPUser(ctx)
			if err != nil {
				return nil, err
			}
			return core.WithUserID(ctx, user.ID), nil
		}}}, nil
	}
	r := mcpregistry.New(&mcp.Implementation{
		Name:    "logger4life",
		Title:   "Logger4Life",
		Version: "0.1.0",
	}, &mcp.ServerOptions{
		// The tool catalog is fixed at startup; clients do not need a
		// subscriptions/listen stream for tool-list changes.
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	}, compiler)

	spec := func(tool mcp.Tool) mcpregistry.Spec[MCPMetadata] {
		return mcpregistry.Spec[MCPMetadata]{Tool: tool, Metadata: MCPMetadata{RequiresUser: true}}
	}
	binding := mcpregistry.Binding{
		Namespace: "github.com/jackc/logger4life/backend/core",
		Errors:    mcpregistry.ErrorPolicy{Name: "safe-core-error", Map: mcpToolError},
	}
	r.Register(spec(mcp.Tool{
		Name:        "list_logs",
		Title:       "List logs",
		Annotations: mcpregistry.ReadOnlyAnnotations(),
		Description: "List an alphabetical page of logs the authenticated user owns or has been shared on. Defaults to 50 records, maximum 100; pass next_cursor as cursor to continue. Pages may be shortened to fit the response size limit.",
	}), mcpregistry.Bind(core.ListLogsPage, app, binding))

	r.Register(spec(mcp.Tool{
		Name:        "get_sql_schema",
		Title:       "Get SQL schema",
		Annotations: mcpregistry.ReadOnlyAnnotations(),
		Description: "Describe the read-only views available for SQL queries (sql_query.logs and sql_query.log_entries) including columns, types, and per-column comments. Call this before writing a query to know what to select.",
	}), mcpregistry.Typed(func(ctx context.Context, _ *mcp.CallToolRequest, _ getSQLSchemaInput) (*mcp.CallToolResult, getSQLSchemaOutput, error) {
		result, err := core.GetSQLSchema.Call(ctx, app, core.GetSQLSchemaParams{})
		if err != nil {
			return nil, getSQLSchemaOutput{}, mcpToolError(ctx, err)
		}
		return nil, getSQLSchemaOutput{Views: result.Views}, nil
	}))

	r.Register(spec(mcp.Tool{
		Name:        "run_sql",
		Title:       "Run SQL query",
		Annotations: mcpregistry.ReadOnlyAnnotations(),
		Description: "Run a read-only SELECT against the sql_query schema views as the authenticated user. Only SELECT statements on the logs and log_entries views are allowed; results are capped at 1000 rows.",
	}), mcpregistry.Typed(func(ctx context.Context, _ *mcp.CallToolRequest, in runSQLInput) (*mcp.CallToolResult, runSQLOutput, error) {
		result, err := core.ExecuteUserSQL.Call(ctx, app, core.ExecuteUserSQLParams{Query: in.Query})
		if err != nil {
			return nil, runSQLOutput{}, mcpToolError(ctx, err)
		}
		return nil, result, nil
	}))

	r.Register(spec(mcp.Tool{
		Name:        "list_saved_queries",
		Title:       "List saved queries",
		Annotations: mcpregistry.ReadOnlyAnnotations(),
		Description: "List an alphabetical page of the authenticated user's saved SQL queries, including query text. Defaults to 50 records, maximum 100; pass next_cursor as cursor to continue. Pages may be shortened to fit the response size limit.",
	}), mcpregistry.Bind(core.ListSavedQueriesPage, app, binding))

	r.Register(spec(mcp.Tool{
		Name:        "run_saved_query",
		Title:       "Run saved query",
		Annotations: mcpregistry.ReadOnlyAnnotations(),
		Description: "Look up a saved query by name and execute it. Equivalent to calling list_saved_queries then run_sql with the matching query_text.",
	}), mcpregistry.Typed(func(ctx context.Context, _ *mcp.CallToolRequest, in runSavedQueryInput) (*mcp.CallToolResult, runSQLOutput, error) {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, runSQLOutput{}, fmt.Errorf("name is required")
		}
		saved, err := core.GetSavedQuery.Call(ctx, app, core.GetSavedQueryParams{Name: name})
		if err != nil {
			if errors.Is(err, core.ErrSavedQueryNotFound) {
				return nil, runSQLOutput{}, fmt.Errorf("no saved query named %q", name)
			}
			return nil, runSQLOutput{}, mcpToolError(ctx, err)
		}
		result, err := core.ExecuteUserSQL.Call(ctx, app, core.ExecuteUserSQLParams{Query: saved.QueryText})
		if err != nil {
			return nil, runSQLOutput{}, mcpToolError(ctx, err)
		}
		return nil, result, nil
	}))

	return r
}
