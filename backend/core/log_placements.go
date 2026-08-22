package core

import (
	"context"
	"errors"
	"github.com/jackc/logger4life/backend/domain"
)

var ErrLogNotPinned = errors.New("log is not pinned to home")

type LogPlacementStore interface {
	UpdateLogPlacement(context.Context, string, string, domain.LogPlacementChange) error
	PinLog(context.Context, string, string, domain.HomePinChange) error
	UpdateLogHomePosition(context.Context, string, string, domain.HomeOrderChange) error
}
type UpdateLogPlacementParams struct {
	LogID    string  `json:"log_id"`
	FolderID *string `json:"folder_id"`
	Position int     `json:"position"`
}

func (p *UpdateLogPlacementParams) Validate() error {
	if e := validID("log_id", p.LogID); e != nil {
		return e
	}
	if e := validOptionalID("folder_id", p.FolderID); e != nil {
		return e
	}
	if p.Position < 0 {
		p.Position = 0
	}
	return nil
}

var UpdateLogPlacement = Define(ActionDef[UpdateLogPlacementParams, struct{}]{Name: "update_log_placement", Description: "Move and reorder a log placement.", Mutating: true, Handler: func(ctx context.Context, c *Core, p UpdateLogPlacementParams) (struct{}, error) {
	u, e := requiredUser(ctx)
	if e == nil {
		e = c.placements.UpdateLogPlacement(ctx, u, p.LogID, domain.LogPlacementChange{FolderID: p.FolderID, Position: p.Position})
	}
	return struct{}{}, e
}})

type PinLogParams struct {
	LogID  string `json:"log_id"`
	Pinned bool   `json:"pinned"`
}

func (p *PinLogParams) Validate() error { return validID("log_id", p.LogID) }

var PinLog = Define(ActionDef[PinLogParams, struct{}]{Name: "pin_log", Description: "Set a log's home pin.", Mutating: true, Handler: func(ctx context.Context, c *Core, p PinLogParams) (struct{}, error) {
	u, e := requiredUser(ctx)
	if e == nil {
		e = c.placements.PinLog(ctx, u, p.LogID, domain.HomePinChange{Pinned: p.Pinned})
	}
	return struct{}{}, e
}})

type UpdateLogHomePositionParams struct {
	LogID        string `json:"log_id"`
	HomePosition int    `json:"home_position"`
}

func (p *UpdateLogHomePositionParams) Validate() error {
	if e := validID("log_id", p.LogID); e != nil {
		return e
	}
	if p.HomePosition < 0 {
		p.HomePosition = 0
	}
	return nil
}

var UpdateLogHomePosition = Define(ActionDef[UpdateLogHomePositionParams, struct{}]{Name: "update_log_home_position", Description: "Reorder a pinned log on home.", Mutating: true, Handler: func(ctx context.Context, c *Core, p UpdateLogHomePositionParams) (struct{}, error) {
	u, e := requiredUser(ctx)
	if e == nil {
		e = c.placements.UpdateLogHomePosition(ctx, u, p.LogID, domain.HomeOrderChange{HomePosition: p.HomePosition})
	}
	return struct{}{}, e
}})
