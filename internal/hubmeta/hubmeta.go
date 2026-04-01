package hubmeta

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/formation-res/open-rtls-hub/internal/config"
	"github.com/formation-res/open-rtls-hub/internal/ids"
	"github.com/formation-res/open-rtls-hub/internal/storage/postgres/sqlcgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const defaultLabel = "open-rtls-hub"

var hostnameLookup = os.Hostname

// Metadata is the effective stable hub identity and operator-facing label.
type Metadata struct {
	HubID string
	Label string
}

// Resolve loads or bootstraps the persisted hub metadata singleton.
func Resolve(ctx context.Context, queries sqlcgen.Querier, cfg config.Config) (Metadata, error) {
	row, err := queries.GetHubMetadata(ctx)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return Metadata{}, fmt.Errorf("load hub metadata: %w", err)
		}
		return createInitial(ctx, queries, cfg)
	}
	stored, err := metadataFromRow(row)
	if err != nil {
		return Metadata{}, err
	}
	return alignExisting(ctx, queries, cfg, stored)
}

func createInitial(ctx context.Context, queries sqlcgen.Querier, cfg config.Config) (Metadata, error) {
	meta := Metadata{
		HubID: cfg.HubID,
		Label: cfg.HubLabel,
	}
	if meta.HubID == "" {
		meta.HubID = ids.NewString()
	}
	if meta.Label == "" {
		meta.Label = defaultHubLabel()
	}
	row, err := queries.CreateHubMetadata(ctx, sqlcgen.CreateHubMetadataParams{
		HubID: mustUUIDParam(meta.HubID),
		Label: meta.Label,
	})
	if err != nil {
		return Metadata{}, fmt.Errorf("persist hub metadata: %w", err)
	}
	return metadataFromRow(row)
}

func alignExisting(ctx context.Context, queries sqlcgen.Querier, cfg config.Config, stored Metadata) (Metadata, error) {
	if cfg.HubID == "" && cfg.HubLabel == "" {
		return stored, nil
	}

	if !cfg.ResetHubID {
		var mismatches []string
		if cfg.HubID != "" && cfg.HubID != stored.HubID {
			mismatches = append(mismatches, fmt.Sprintf("HUB_ID stored=%q configured=%q", stored.HubID, cfg.HubID))
		}
		if cfg.HubLabel != "" && cfg.HubLabel != stored.Label {
			mismatches = append(mismatches, fmt.Sprintf("HUB_LABEL stored=%q configured=%q", stored.Label, cfg.HubLabel))
		}
		if len(mismatches) > 0 {
			return Metadata{}, fmt.Errorf("hub metadata mismatch: %s; set RESET_HUB_ID=true to overwrite the stored values", strings.Join(mismatches, ", "))
		}
		return stored, nil
	}

	updated := stored
	if cfg.HubID != "" {
		updated.HubID = cfg.HubID
	}
	if cfg.HubLabel != "" {
		updated.Label = cfg.HubLabel
	}
	row, err := queries.UpdateHubMetadata(ctx, sqlcgen.UpdateHubMetadataParams{
		HubID: mustUUIDParam(updated.HubID),
		Label: updated.Label,
	})
	if err != nil {
		return Metadata{}, fmt.Errorf("update hub metadata: %w", err)
	}
	return metadataFromRow(row)
}

func defaultHubLabel() string {
	host, err := hostnameLookup()
	if err != nil {
		return defaultLabel
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return defaultLabel
	}
	return host
}

func metadataFromRow(row sqlcgen.HubMetadatum) (Metadata, error) {
	hubID, err := uuidFromPG(row.HubID)
	if err != nil {
		return Metadata{}, fmt.Errorf("decode hub metadata id: %w", err)
	}
	return Metadata{
		HubID: hubID.String(),
		Label: row.Label,
	}, nil
}

func uuidFromPG(v pgtype.UUID) (uuid.UUID, error) {
	if !v.Valid {
		return uuid.UUID{}, fmt.Errorf("uuid is null")
	}
	return uuid.FromBytes(v.Bytes[:])
}

func mustUUIDParam(id string) pgtype.UUID {
	parsed := uuid.MustParse(id)
	var out pgtype.UUID
	copy(out.Bytes[:], parsed[:])
	out.Valid = true
	return out
}
