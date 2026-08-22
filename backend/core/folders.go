package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrFolderNotFound       = errors.New("folder not found")
	ErrParentFolderNotFound = errors.New("parent folder not found")
	ErrFolderCycle          = errors.New("cannot move a folder into its own descendant")
	ErrFolderOwnParent      = errors.New("folder cannot be its own parent")
	ErrFolderNotEmpty       = errors.New("folder is not empty")
)

type Folder struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	ParentFolderID *string   `json:"parent_folder_id"`
	Position       int       `json:"position"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type FolderStore interface {
	CreateFolder(context.Context, string, string, *string) (Folder, error)
	ListFolders(context.Context, string) ([]Folder, error)
	RenameFolder(context.Context, string, string, string) (Folder, error)
	MoveFolder(context.Context, string, string, *string, int) error
	DeleteFolder(context.Context, string, string) error
}

func validFolderName(name string) error {
	if len(strings.TrimSpace(name)) < 1 || len(strings.TrimSpace(name)) > 100 {
		return fmt.Errorf("name must be 1-100 characters")
	}
	return nil
}
func requiredUser(ctx context.Context) (string, error) {
	id, ok := UserIDFromContext(ctx)
	if !ok {
		return "", ErrUnauthenticated
	}
	return id, nil
}

type CreateFolderParams struct {
	Name           string  `json:"name"`
	ParentFolderID *string `json:"parent_folder_id"`
}

func (p *CreateFolderParams) Validate() error {
	if e := validOptionalID("parent_folder_id", p.ParentFolderID); e != nil {
		return e
	}
	p.Name = strings.TrimSpace(p.Name)
	return validFolderName(p.Name)
}

var CreateFolder = Define(ActionDef[CreateFolderParams, Folder]{Name: "create_folder", Description: "Create a folder.", Mutating: true, Handler: func(ctx context.Context, c *Core, p CreateFolderParams) (Folder, error) {
	id, e := requiredUser(ctx)
	if e != nil {
		return Folder{}, e
	}
	return c.folders.CreateFolder(ctx, id, p.Name, p.ParentFolderID)
}})

type ListFoldersParams struct{}

var ListFolders = Define(ActionDef[ListFoldersParams, []Folder]{Name: "list_folders", Description: "List the current user's folders.", Handler: func(ctx context.Context, c *Core, _ ListFoldersParams) ([]Folder, error) {
	id, e := requiredUser(ctx)
	if e != nil {
		return nil, e
	}
	return c.folders.ListFolders(ctx, id)
}})

type RenameFolderParams struct {
	FolderID string `json:"folder_id"`
	Name     string `json:"name"`
}

func (p *RenameFolderParams) Validate() error {
	if e := validID("folder_id", p.FolderID); e != nil {
		return e
	}
	p.Name = strings.TrimSpace(p.Name)
	return validFolderName(p.Name)
}

var RenameFolder = Define(ActionDef[RenameFolderParams, Folder]{Name: "rename_folder", Description: "Rename a folder.", Mutating: true, Handler: func(ctx context.Context, c *Core, p RenameFolderParams) (Folder, error) {
	id, e := requiredUser(ctx)
	if e != nil {
		return Folder{}, e
	}
	return c.folders.RenameFolder(ctx, id, p.FolderID, p.Name)
}})

type MoveFolderParams struct {
	FolderID       string  `json:"folder_id"`
	ParentFolderID *string `json:"parent_folder_id"`
	Position       int     `json:"position"`
}

func (p *MoveFolderParams) Validate() error {
	if e := validID("folder_id", p.FolderID); e != nil {
		return e
	}
	if e := validOptionalID("parent_folder_id", p.ParentFolderID); e != nil {
		return e
	}
	if p.Position < 0 {
		p.Position = 0
	}
	return nil
}

var MoveFolder = Define(ActionDef[MoveFolderParams, struct{}]{Name: "move_folder", Description: "Move and reorder a folder.", Mutating: true, Handler: func(ctx context.Context, c *Core, p MoveFolderParams) (struct{}, error) {
	id, e := requiredUser(ctx)
	if e == nil {
		e = c.folders.MoveFolder(ctx, id, p.FolderID, p.ParentFolderID, p.Position)
	}
	return struct{}{}, e
}})

type DeleteFolderParams struct {
	FolderID string `json:"folder_id"`
}

func (p *DeleteFolderParams) Validate() error { return validID("folder_id", p.FolderID) }

var DeleteFolder = Define(ActionDef[DeleteFolderParams, struct{}]{Name: "delete_folder", Description: "Delete an empty folder.", Mutating: true, Handler: func(ctx context.Context, c *Core, p DeleteFolderParams) (struct{}, error) {
	id, e := requiredUser(ctx)
	if e == nil {
		e = c.folders.DeleteFolder(ctx, id, p.FolderID)
	}
	return struct{}{}, e
}})
