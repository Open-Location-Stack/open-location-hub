package hubmeta

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/formation-res/open-rtls-hub/internal/config"
	"github.com/formation-res/open-rtls-hub/internal/storage/postgres/sqlcgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestResolveBootstrapsGeneratedDefaults(t *testing.T) {
	hostnameLookup = func() (string, error) { return "hub-host", nil }
	t.Cleanup(func() { hostnameLookup = os.Hostname })

	fake := &fakeQueries{getErr: pgx.ErrNoRows}
	meta, err := Resolve(context.Background(), fake, config.Config{})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if _, err := uuid.Parse(meta.HubID); err != nil {
		t.Fatalf("expected generated hub id to be a UUID, got %q", meta.HubID)
	}
	if meta.Label == "" {
		t.Fatal("expected non-empty default label")
	}
	if meta.Label != "hub-host" {
		t.Fatalf("expected hostname default label, got %q", meta.Label)
	}
	if fake.created == nil {
		t.Fatal("expected hub metadata row to be created")
	}
}

func TestResolveFallsBackToProductNameWhenHostnameUnavailable(t *testing.T) {
	hostnameLookup = func() (string, error) { return "", errors.New("no hostname") }
	t.Cleanup(func() { hostnameLookup = os.Hostname })

	fake := &fakeQueries{getErr: pgx.ErrNoRows}
	meta, err := Resolve(context.Background(), fake, config.Config{})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if meta.Label != "open-rtls-hub" {
		t.Fatalf("expected fallback label, got %q", meta.Label)
	}
}

func TestResolveBootstrapsConfiguredValues(t *testing.T) {
	t.Parallel()

	id := uuid.NewString()
	fake := &fakeQueries{getErr: pgx.ErrNoRows}
	meta, err := Resolve(context.Background(), fake, config.Config{HubID: id, HubLabel: "alpha"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if meta.HubID != id || meta.Label != "alpha" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
}

func TestResolveUsesStoredValuesWhenEnvMissing(t *testing.T) {
	t.Parallel()

	stored := hubRow("4f630dd4-e5f2-4398-9970-c63cad9bc109", "stored")
	meta, err := Resolve(context.Background(), &fakeQueries{row: stored}, config.Config{})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if meta.HubID != "4f630dd4-e5f2-4398-9970-c63cad9bc109" || meta.Label != "stored" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
}

func TestResolveFailsOnMismatchedExistingID(t *testing.T) {
	t.Parallel()

	_, err := Resolve(context.Background(), &fakeQueries{row: hubRow("4f630dd4-e5f2-4398-9970-c63cad9bc109", "stored")}, config.Config{
		HubID: "f824c6a9-82bf-48bc-8bb0-5d1f6e673262",
	})
	if err == nil || !contains(err.Error(), "HUB_ID") {
		t.Fatalf("expected HUB_ID mismatch error, got %v", err)
	}
}

func TestResolveFailsOnMismatchedExistingLabel(t *testing.T) {
	t.Parallel()

	_, err := Resolve(context.Background(), &fakeQueries{row: hubRow("4f630dd4-e5f2-4398-9970-c63cad9bc109", "stored")}, config.Config{
		HubLabel: "changed",
	})
	if err == nil || !contains(err.Error(), "HUB_LABEL") {
		t.Fatalf("expected HUB_LABEL mismatch error, got %v", err)
	}
}

func TestResolveOverwritesProvidedFieldsWhenResetEnabled(t *testing.T) {
	t.Parallel()

	fake := &fakeQueries{row: hubRow("4f630dd4-e5f2-4398-9970-c63cad9bc109", "stored")}
	meta, err := Resolve(context.Background(), fake, config.Config{
		HubID:      "f824c6a9-82bf-48bc-8bb0-5d1f6e673262",
		ResetHubID: true,
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if meta.HubID != "f824c6a9-82bf-48bc-8bb0-5d1f6e673262" || meta.Label != "stored" {
		t.Fatalf("unexpected metadata after reset: %+v", meta)
	}
	if fake.updated == nil || fake.updated.Label != "stored" {
		t.Fatalf("expected stored label to be preserved, got %+v", fake.updated)
	}
}

func TestResolvePropagatesLoadFailure(t *testing.T) {
	t.Parallel()

	_, err := Resolve(context.Background(), &fakeQueries{getErr: errors.New("db down")}, config.Config{})
	if err == nil || !contains(err.Error(), "load hub metadata") {
		t.Fatalf("expected wrapped load error, got %v", err)
	}
}

type fakeQueries struct {
	row     sqlcgen.HubMetadatum
	getErr  error
	created *sqlcgen.CreateHubMetadataParams
	updated *sqlcgen.UpdateHubMetadataParams
}

func (f *fakeQueries) GetHubMetadata(context.Context) (sqlcgen.HubMetadatum, error) {
	if f.getErr != nil {
		return sqlcgen.HubMetadatum{}, f.getErr
	}
	return f.row, nil
}

func (f *fakeQueries) CreateHubMetadata(_ context.Context, params sqlcgen.CreateHubMetadataParams) (sqlcgen.HubMetadatum, error) {
	f.created = &params
	return sqlcgen.HubMetadatum{
		SingletonID: 1,
		HubID:       params.HubID,
		Label:       params.Label,
	}, nil
}

func (f *fakeQueries) UpdateHubMetadata(_ context.Context, params sqlcgen.UpdateHubMetadataParams) (sqlcgen.HubMetadatum, error) {
	f.updated = &params
	return sqlcgen.HubMetadatum{
		SingletonID: 1,
		HubID:       params.HubID,
		Label:       params.Label,
	}, nil
}

func (*fakeQueries) CreateFence(context.Context, sqlcgen.CreateFenceParams) (sqlcgen.Fence, error) {
	panic("unexpected call")
}

func (*fakeQueries) CreateProvider(context.Context, sqlcgen.CreateProviderParams) (sqlcgen.Provider, error) {
	panic("unexpected call")
}

func (*fakeQueries) CreateTrackable(context.Context, sqlcgen.CreateTrackableParams) (sqlcgen.Trackable, error) {
	panic("unexpected call")
}

func (*fakeQueries) CreateZone(context.Context, sqlcgen.CreateZoneParams) (sqlcgen.Zone, error) {
	panic("unexpected call")
}

func (*fakeQueries) DeleteFence(context.Context, pgtype.UUID) (int64, error) {
	panic("unexpected call")
}

func (*fakeQueries) DeleteProvider(context.Context, string) (int64, error) {
	panic("unexpected call")
}

func (*fakeQueries) DeleteTrackable(context.Context, pgtype.UUID) (int64, error) {
	panic("unexpected call")
}

func (*fakeQueries) DeleteZone(context.Context, pgtype.UUID) (int64, error) {
	panic("unexpected call")
}

func (*fakeQueries) GetFence(context.Context, pgtype.UUID) (sqlcgen.Fence, error) {
	panic("unexpected call")
}

func (*fakeQueries) GetProvider(context.Context, string) (sqlcgen.Provider, error) {
	panic("unexpected call")
}

func (*fakeQueries) GetTrackable(context.Context, pgtype.UUID) (sqlcgen.Trackable, error) {
	panic("unexpected call")
}

func (*fakeQueries) GetZone(context.Context, pgtype.UUID) (sqlcgen.Zone, error) {
	panic("unexpected call")
}

func (*fakeQueries) ListFences(context.Context) ([]sqlcgen.Fence, error) {
	panic("unexpected call")
}

func (*fakeQueries) ListProviders(context.Context) ([]sqlcgen.Provider, error) {
	panic("unexpected call")
}

func (*fakeQueries) ListTrackables(context.Context) ([]sqlcgen.Trackable, error) {
	panic("unexpected call")
}

func (*fakeQueries) ListZones(context.Context) ([]sqlcgen.Zone, error) {
	panic("unexpected call")
}

func (*fakeQueries) UpdateFence(context.Context, sqlcgen.UpdateFenceParams) (sqlcgen.Fence, error) {
	panic("unexpected call")
}

func (*fakeQueries) UpdateProvider(context.Context, sqlcgen.UpdateProviderParams) (sqlcgen.Provider, error) {
	panic("unexpected call")
}

func (*fakeQueries) UpdateTrackable(context.Context, sqlcgen.UpdateTrackableParams) (sqlcgen.Trackable, error) {
	panic("unexpected call")
}

func (*fakeQueries) UpdateZone(context.Context, sqlcgen.UpdateZoneParams) (sqlcgen.Zone, error) {
	panic("unexpected call")
}

func hubRow(id string, label string) sqlcgen.HubMetadatum {
	parsed := uuid.MustParse(id)
	var pgID pgtype.UUID
	copy(pgID.Bytes[:], parsed[:])
	pgID.Valid = true
	return sqlcgen.HubMetadatum{
		SingletonID: 1,
		HubID:       pgID,
		Label:       label,
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
