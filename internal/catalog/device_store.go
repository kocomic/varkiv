package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s *Store) CreateDevice(ctx context.Context, in NewDevice) (Device, error) {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.OSFamily) == "" {
		return Device{}, errors.New("device name and os_family are required")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if in.Status != "active" && in.Status != "offline" {
		return Device{}, errors.New("new device status must be active or offline")
	}
	if in.DeviceProfileID != "" {
		profile, err := s.GetDeviceProfile(ctx, in.DeviceProfileID)
		if err != nil {
			return Device{}, err
		}
		if !profile.Enabled {
			return Device{}, ErrPairingDeviceProfileUnavailable
		}
		if !pairingProfileCompatible(profile.OSFamily, profile.Architecture, in.OSFamily, in.Architecture) {
			return Device{}, ErrPairingDeviceProfileIncompatible
		}
	}
	capabilities, _ := json.Marshal(in.Capabilities)
	now := nowText()
	_, err := s.db.ExecContext(ctx, `INSERT INTO devices(id,name,device_profile_id,os_family,distribution,architecture,agent_version,status,capabilities_json,created_at,updated_at,last_seen_at,revoked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.ID, strings.TrimSpace(in.Name), strings.TrimSpace(in.DeviceProfileID), strings.TrimSpace(in.OSFamily), strings.TrimSpace(in.Distribution), strings.TrimSpace(in.Architecture), strings.TrimSpace(in.AgentVersion), in.Status, string(capabilities), now, now, now, "")
	if err != nil {
		return Device{}, err
	}
	return s.GetDevice(ctx, in.ID)
}

func (s *Store) GetDevice(ctx context.Context, id string) (Device, error) {
	var d Device
	var capabilities, created, updated, seen, revoked string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,device_profile_id,os_family,distribution,architecture,agent_version,status,capabilities_json,created_at,updated_at,last_seen_at,revoked_at FROM devices WHERE id=?`, id).Scan(
		&d.ID, &d.Name, &d.DeviceProfileID, &d.OSFamily, &d.Distribution, &d.Architecture, &d.AgentVersion, &d.Status, &capabilities, &created, &updated, &seen, &revoked)
	if err != nil {
		return Device{}, err
	}
	_ = json.Unmarshal([]byte(capabilities), &d.Capabilities)
	if d.Capabilities == nil {
		d.Capabilities = map[string]bool{}
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	d.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	d.LastSeenAt, _ = time.Parse(time.RFC3339Nano, seen)
	d.RevokedAt, _ = time.Parse(time.RFC3339Nano, revoked)
	return d, nil
}

func (s *Store) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM devices ORDER BY lower(name),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	devices := make([]Device, 0, len(ids))
	for _, id := range ids {
		d, getErr := s.GetDevice(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func (s *Store) UpdateDevice(ctx context.Context, id string, in NewDevice) (Device, error) {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.OSFamily) == "" {
		return Device{}, errors.New("device name and os_family are required")
	}
	current, err := s.GetDevice(ctx, id)
	if err != nil {
		return Device{}, err
	}
	if current.Status == "revoked" {
		return Device{}, ErrDeviceRevoked
	}
	if in.Status == "" {
		in.Status = current.Status
	}
	if in.Status != "active" && in.Status != "offline" && in.Status != "revoked" {
		return Device{}, errors.New("device status must be active, offline, or revoked")
	}
	if in.Status == "revoked" {
		return Device{}, ErrDeviceRevocationRequired
	}
	in.DeviceProfileID = strings.TrimSpace(in.DeviceProfileID)
	in.OSFamily = strings.TrimSpace(in.OSFamily)
	in.Architecture = strings.TrimSpace(in.Architecture)
	identityChanged := in.DeviceProfileID != current.DeviceProfileID || in.OSFamily != current.OSFamily || in.Architecture != current.Architecture
	if identityChanged {
		var activeTokens int
		if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM client_tokens WHERE device_id=? AND revoked_at='' AND expires_at>?`, id, nowText()).Scan(&activeTokens); err != nil {
			return Device{}, err
		}
		if activeTokens > 0 {
			return Device{}, ErrPairedDeviceIdentityInUse
		}
	}
	if in.DeviceProfileID != "" {
		profile, profileErr := s.GetDeviceProfile(ctx, in.DeviceProfileID)
		if profileErr != nil {
			return Device{}, profileErr
		}
		if in.DeviceProfileID != current.DeviceProfileID && !profile.Enabled {
			return Device{}, ErrPairingDeviceProfileUnavailable
		}
		if !pairingProfileCompatible(profile.OSFamily, profile.Architecture, in.OSFamily, in.Architecture) {
			return Device{}, ErrPairingDeviceProfileIncompatible
		}
	}
	capabilities, _ := json.Marshal(in.Capabilities)
	revokedAt := ""
	if !current.RevokedAt.IsZero() {
		revokedAt = current.RevokedAt.Format(time.RFC3339Nano)
	}
	if in.Status == "revoked" && revokedAt == "" {
		revokedAt = nowText()
	}
	result, err := s.db.ExecContext(ctx, `UPDATE devices SET name=?,device_profile_id=?,os_family=?,distribution=?,architecture=?,agent_version=?,status=?,capabilities_json=?,revoked_at=?,updated_at=? WHERE id=?`,
		strings.TrimSpace(in.Name), strings.TrimSpace(in.DeviceProfileID), strings.TrimSpace(in.OSFamily), strings.TrimSpace(in.Distribution), strings.TrimSpace(in.Architecture), strings.TrimSpace(in.AgentVersion), in.Status, string(capabilities), revokedAt, nowText(), id)
	if err != nil {
		return Device{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return Device{}, sql.ErrNoRows
	}
	return s.GetDevice(ctx, id)
}

func (s *Store) DeleteDevice(ctx context.Context, id string) error {
	var revisions int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM save_revisions WHERE device_id=?`, id).Scan(&revisions); err != nil {
		return err
	}
	if revisions > 0 {
		return ErrDeviceHasSaveRevisions
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteAll(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM games`)
	return err
}
