package catalog

import (
	"database/sql"
	"errors"
	"fmt"
)

func (s *Store) migrate() error {
	migrations := []string{`
CREATE TABLE IF NOT EXISTS works (
  id TEXT PRIMARY KEY,
  default_title TEXT NOT NULL,
  platform TEXT NOT NULL,
  primary_edition_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS localized_titles (
  owner_type TEXT NOT NULL CHECK(owner_type IN ('work','edition')),
  owner_id TEXT NOT NULL,
  locale TEXT NOT NULL,
  title TEXT NOT NULL,
  sort_title TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(owner_type, owner_id, locale)
);
CREATE TABLE IF NOT EXISTS editions (
  id TEXT PRIMARY KEY,
  work_id TEXT NOT NULL REFERENCES works(id) ON DELETE CASCADE,
  default_title TEXT NOT NULL,
  edition_type TEXT NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  languages_json TEXT NOT NULL DEFAULT '[]',
  author TEXT NOT NULL DEFAULT '',
  save_namespace TEXT NOT NULL UNIQUE,
  serial TEXT NOT NULL DEFAULT '',
  product_code TEXT NOT NULL DEFAULT '',
  title_id TEXT NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS artifacts (
  id TEXT PRIMARY KEY,
  edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
  path TEXT NOT NULL UNIQUE,
  role TEXT NOT NULL DEFAULT 'rom',
  disc_index INTEGER NOT NULL DEFAULT 0,
  size INTEGER NOT NULL DEFAULT 0,
  sha256 TEXT NOT NULL DEFAULT '',
  missing INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_editions_work ON editions(work_id, sort_order);
CREATE INDEX IF NOT EXISTS idx_artifacts_sha ON artifacts(sha256);
CREATE TABLE IF NOT EXISTS source_records (
  id TEXT PRIMARY KEY,
  source_type TEXT NOT NULL,
  source_path TEXT NOT NULL,
  external_id TEXT NOT NULL DEFAULT '',
  edition_id TEXT REFERENCES editions(id) ON DELETE SET NULL,
  raw_json TEXT NOT NULL DEFAULT '{}',
  imported_at TEXT NOT NULL
);
`, `
CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  os_family TEXT NOT NULL,
  distribution TEXT NOT NULL DEFAULT '',
  architecture TEXT NOT NULL DEFAULT '',
  capabilities_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS save_revisions (
  id TEXT PRIMARY KEY,
  edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
  device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE RESTRICT,
  driver_id TEXT NOT NULL DEFAULT '',
  relative_path TEXT NOT NULL,
  scope_type TEXT NOT NULL DEFAULT 'game',
  scope_key TEXT NOT NULL DEFAULT '',
  checksum TEXT NOT NULL,
  size INTEGER NOT NULL,
  blob_path TEXT NOT NULL,
  base_revision_id TEXT NOT NULL DEFAULT '',
  conflict INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_save_revisions_unit ON save_revisions(edition_id,driver_id,scope_type,scope_key,relative_path,created_at DESC);
CREATE INDEX IF NOT EXISTS idx_save_revisions_checksum ON save_revisions(checksum);
`, `
ALTER TABLE artifacts ADD COLUMN storage_kind TEXT NOT NULL DEFAULT 'library';
ALTER TABLE artifacts ADD COLUMN source_path TEXT NOT NULL DEFAULT '';
ALTER TABLE artifacts ADD COLUMN original_name TEXT NOT NULL DEFAULT '';
UPDATE artifacts SET source_path=path, original_name=path WHERE source_path='';
CREATE TABLE IF NOT EXISTS media_assets (
  id TEXT PRIMARY KEY,
  work_id TEXT REFERENCES works(id) ON DELETE CASCADE,
  edition_id TEXT REFERENCES editions(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  storage_kind TEXT NOT NULL CHECK(storage_kind IN ('library','managed')),
  path TEXT NOT NULL,
  source_path TEXT NOT NULL DEFAULT '',
  original_name TEXT NOT NULL,
  mime_type TEXT NOT NULL DEFAULT 'application/octet-stream',
  size INTEGER NOT NULL DEFAULT 0,
  sha256 TEXT NOT NULL DEFAULT '',
  locale TEXT NOT NULL DEFAULT '',
  source_type TEXT NOT NULL DEFAULT 'upload',
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  CHECK((work_id IS NOT NULL AND edition_id IS NULL) OR (work_id IS NULL AND edition_id IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS idx_media_work ON media_assets(work_id,kind,sort_order);
CREATE INDEX IF NOT EXISTS idx_media_edition ON media_assets(edition_id,kind,sort_order);
CREATE INDEX IF NOT EXISTS idx_media_sha ON media_assets(sha256);
`, `
CREATE TABLE IF NOT EXISTS series (
  id TEXT PRIMARY KEY,
  default_title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS series_titles (
  series_id TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
  locale TEXT NOT NULL,
  title TEXT NOT NULL,
  sort_title TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(series_id, locale)
);
CREATE TABLE IF NOT EXISTS series_members (
  series_id TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
  work_id TEXT NOT NULL REFERENCES works(id) ON DELETE CASCADE,
  relation_type TEXT NOT NULL DEFAULT 'mainline' CHECK(relation_type IN ('mainline','port','remake','spinoff','collection','other')),
  sort_order INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(series_id, work_id)
);
CREATE INDEX IF NOT EXISTS idx_series_members_work ON series_members(work_id);
CREATE INDEX IF NOT EXISTS idx_series_members_order ON series_members(series_id,sort_order,work_id);
`, `
CREATE UNIQUE INDEX IF NOT EXISTS idx_artifacts_sha_unique ON artifacts(sha256) WHERE sha256<>'';
`, `
CREATE TABLE IF NOT EXISTS library_sources (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('rom_directory','pegasus','esde')),
  root_path TEXT NOT NULL DEFAULT '',
  metadata_path TEXT NOT NULL DEFAULT '',
  platform TEXT NOT NULL,
  metadata_locale TEXT NOT NULL DEFAULT '',
  rom_storage_policy TEXT NOT NULL CHECK(rom_storage_policy IN ('reference','copy')),
  media_storage_policy TEXT NOT NULL CHECK(media_storage_policy IN ('reference','copy','ignore')),
  enabled INTEGER NOT NULL DEFAULT 1,
  last_scan_at TEXT NOT NULL DEFAULT '',
  last_scan_status TEXT NOT NULL DEFAULT 'never',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_library_sources_platform ON library_sources(platform,kind,name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_library_sources_identity ON library_sources(kind,root_path,metadata_path,platform);
CREATE TABLE IF NOT EXISTS source_scans (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL REFERENCES library_sources(id) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK(status IN ('scanning','ready','committed','failed','stale')),
  requested_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL DEFAULT '',
  candidate_count INTEGER NOT NULL DEFAULT 0,
  importable_count INTEGER NOT NULL DEFAULT 0,
  missing_count INTEGER NOT NULL DEFAULT 0,
  duplicate_count INTEGER NOT NULL DEFAULT 0,
  conflict_count INTEGER NOT NULL DEFAULT 0,
  preview_token_hash TEXT NOT NULL DEFAULT '',
  failure_code TEXT NOT NULL DEFAULT '',
  failure_detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_source_scans_source ON source_scans(source_id,requested_at DESC,id DESC);
`, `
CREATE TABLE IF NOT EXISTS package_profiles (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  frontend TEXT NOT NULL CHECK(frontend IN ('pegasus','es-de')),
  target TEXT NOT NULL,
  locale TEXT NOT NULL DEFAULT 'zh-CN',
  file_mode TEXT NOT NULL CHECK(file_mode IN ('copy','hardlink','reference')),
  output_slug TEXT NOT NULL UNIQUE,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS package_config_templates (
  id TEXT PRIMARY KEY,
  profile_id TEXT NOT NULL REFERENCES package_profiles(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  scope TEXT NOT NULL CHECK(scope IN ('package','platform','edition')),
  output_path TEXT NOT NULL,
  body TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_package_templates_profile ON package_config_templates(profile_id,sort_order,id);
CREATE TABLE IF NOT EXISTS package_plans (
  id TEXT PRIMARY KEY,
  profile_id TEXT NOT NULL REFERENCES package_profiles(id) ON DELETE RESTRICT,
  fingerprint TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('ready','built','stale','failed')),
  plan_json TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_package_plans_profile ON package_plans(profile_id,created_at DESC,id DESC);
CREATE TABLE IF NOT EXISTS package_releases (
  id TEXT PRIMARY KEY,
  profile_id TEXT NOT NULL REFERENCES package_profiles(id) ON DELETE RESTRICT,
  plan_id TEXT NOT NULL REFERENCES package_plans(id) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK(status IN ('succeeded','failed')),
  output_slug TEXT NOT NULL,
  result_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_package_releases_profile ON package_releases(profile_id,created_at DESC,id DESC);
`, `
ALTER TABLE package_profiles ADD COLUMN device_profile_id TEXT NOT NULL DEFAULT '';
ALTER TABLE package_profiles ADD COLUMN frontend_adapter_id TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS frontend_adapters (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  format TEXT NOT NULL,
  contract_version INTEGER NOT NULL DEFAULT 1,
  capabilities_json TEXT NOT NULL DEFAULT '{}',
  support_level TEXT NOT NULL CHECK(support_level IN ('catalogued','package-tested','hardware-tested','sync-tested')),
  evidence_json TEXT NOT NULL DEFAULT '{}',
  builtin INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_frontend_adapters_format ON frontend_adapters(format) WHERE enabled=1;
CREATE TABLE IF NOT EXISTS device_profiles (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  target TEXT NOT NULL,
  os_family TEXT NOT NULL,
  distribution TEXT NOT NULL DEFAULT '',
  architecture TEXT NOT NULL DEFAULT '',
  path_style TEXT NOT NULL CHECK(path_style IN ('posix','windows','android-uri')),
  case_sensitive INTEGER NOT NULL DEFAULT 1,
  max_path INTEGER NOT NULL DEFAULT 255,
  illegal_characters TEXT NOT NULL DEFAULT '',
  supports_hardlink INTEGER NOT NULL DEFAULT 0,
  supports_hooks INTEGER NOT NULL DEFAULT 0,
  default_frontend_id TEXT REFERENCES frontend_adapters(id) ON DELETE RESTRICT,
  paths_json TEXT NOT NULL DEFAULT '{}',
  support_level TEXT NOT NULL CHECK(support_level IN ('catalogued','package-tested','hardware-tested','sync-tested')),
  evidence_json TEXT NOT NULL DEFAULT '{}',
  builtin INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_device_profiles_target ON device_profiles(target,enabled,name);
CREATE TABLE IF NOT EXISTS emulator_drivers (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  family TEXT NOT NULL,
  contract_version INTEGER NOT NULL DEFAULT 1,
  platforms_json TEXT NOT NULL DEFAULT '[]',
  targets_json TEXT NOT NULL DEFAULT '[]',
  launch_json TEXT NOT NULL DEFAULT '{}',
  save_json TEXT NOT NULL DEFAULT '{}',
  config_paths_json TEXT NOT NULL DEFAULT '{}',
  support_level TEXT NOT NULL CHECK(support_level IN ('catalogued','package-tested','hardware-tested','sync-tested')),
  evidence_json TEXT NOT NULL DEFAULT '{}',
  builtin INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_emulator_drivers_family ON emulator_drivers(family,enabled,name);
CREATE TABLE IF NOT EXISTS retroarch_cores (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  library_names_json TEXT NOT NULL DEFAULT '[]',
  platforms_json TEXT NOT NULL DEFAULT '[]',
  support_level TEXT NOT NULL CHECK(support_level IN ('catalogued','package-tested','hardware-tested','sync-tested')),
  evidence_json TEXT NOT NULL DEFAULT '{}',
  builtin INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS core_mappings (
  id TEXT PRIMARY KEY,
  scope_type TEXT NOT NULL CHECK(scope_type IN ('global','platform','device_profile','edition')),
  scope_key TEXT NOT NULL DEFAULT '',
  platform_id TEXT NOT NULL,
  core_id TEXT NOT NULL REFERENCES retroarch_cores(id) ON DELETE RESTRICT,
  priority INTEGER NOT NULL DEFAULT 0,
  notes TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(scope_type,scope_key,platform_id)
);
CREATE INDEX IF NOT EXISTS idx_core_mappings_resolve ON core_mappings(platform_id,scope_type,scope_key,priority DESC);
CREATE TABLE IF NOT EXISTS launch_bindings (
  id TEXT PRIMARY KEY,
  edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
  device_profile_id TEXT REFERENCES device_profiles(id) ON DELETE RESTRICT,
  driver_id TEXT NOT NULL REFERENCES emulator_drivers(id) ON DELETE RESTRICT,
  frontend_adapter_id TEXT REFERENCES frontend_adapters(id) ON DELETE RESTRICT,
  core_id TEXT REFERENCES retroarch_cores(id) ON DELETE RESTRICT,
  arguments_json TEXT NOT NULL DEFAULT '[]',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_launch_bindings_edition ON launch_bindings(edition_id,enabled,device_profile_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_launch_bindings_scope ON launch_bindings(edition_id,COALESCE(device_profile_id,''));
`, `
ALTER TABLE devices ADD COLUMN device_profile_id TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN agent_version TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','offline','revoked'));
ALTER TABLE devices ADD COLUMN revoked_at TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS idx_save_revisions_unit;
DROP INDEX IF EXISTS idx_save_revisions_checksum;
ALTER TABLE save_revisions RENAME TO legacy_save_revisions;

CREATE TABLE save_streams (
  id TEXT PRIMARY KEY,
  namespace TEXT NOT NULL UNIQUE,
  owner_type TEXT NOT NULL CHECK(owner_type IN ('edition','platform','container')),
  owner_key TEXT NOT NULL,
  driver_id TEXT NOT NULL DEFAULT '',
  portability TEXT NOT NULL DEFAULT 'driver-dependent' CHECK(portability IN ('portable','core-dependent','driver-dependent','device-dependent')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(owner_type,owner_key,driver_id)
);
CREATE TABLE save_stream_editions (
  stream_id TEXT NOT NULL REFERENCES save_streams(id) ON DELETE CASCADE,
  edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
  compatibility TEXT NOT NULL DEFAULT 'native' CHECK(compatibility IN ('native','verified','manual')),
  created_at TEXT NOT NULL,
  PRIMARY KEY(stream_id,edition_id)
);
CREATE INDEX idx_save_stream_editions_edition ON save_stream_editions(edition_id,stream_id);
CREATE TABLE save_revisions (
  id TEXT PRIMARY KEY,
  stream_id TEXT NOT NULL REFERENCES save_streams(id) ON DELETE CASCADE,
  device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE RESTRICT,
  parent_revision_id TEXT REFERENCES save_revisions(id) ON DELETE SET NULL,
  content_hash TEXT NOT NULL,
  total_size INTEGER NOT NULL,
  file_count INTEGER NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('current','superseded','conflict','quarantined')),
  created_at TEXT NOT NULL
);
CREATE INDEX idx_save_revisions_stream ON save_revisions(stream_id,created_at DESC,id DESC);
CREATE INDEX idx_save_revisions_content ON save_revisions(content_hash);
CREATE TABLE save_files (
  id TEXT PRIMARY KEY,
  revision_id TEXT NOT NULL REFERENCES save_revisions(id) ON DELETE CASCADE,
  logical_path TEXT NOT NULL,
  checksum TEXT NOT NULL,
  size INTEGER NOT NULL,
  blob_path TEXT NOT NULL,
  mtime_ns INTEGER NOT NULL DEFAULT 0,
  mode INTEGER NOT NULL DEFAULT 0,
  UNIQUE(revision_id,logical_path)
);
CREATE INDEX idx_save_files_checksum ON save_files(checksum);

INSERT INTO save_streams(id,namespace,owner_type,owner_key,driver_id,portability,created_at,updated_at)
SELECT
  'legacy-stream-' || MIN(r.id),
  CASE WHEN r.scope_type='game'
    THEN e.save_namespace || ':' || CASE WHEN r.driver_id='' THEN 'manual' ELSE r.driver_id END
    ELSE r.scope_type || ':' || r.scope_key || ':' || CASE WHEN r.driver_id='' THEN 'manual' ELSE r.driver_id END END,
  CASE WHEN r.scope_type='game' THEN 'edition' ELSE r.scope_type END,
  CASE WHEN r.scope_type='game' THEN r.edition_id ELSE r.scope_key END,
  CASE WHEN r.driver_id='' THEN 'manual' ELSE r.driver_id END,
  'driver-dependent',
  MIN(r.created_at),MAX(r.created_at)
FROM legacy_save_revisions r
JOIN editions e ON e.id=r.edition_id
GROUP BY r.edition_id,r.driver_id,r.scope_type,r.scope_key;

INSERT INTO save_stream_editions(stream_id,edition_id,compatibility,created_at)
SELECT s.id,r.edition_id,'native',MIN(r.created_at)
FROM legacy_save_revisions r
JOIN save_streams s ON s.owner_type=CASE WHEN r.scope_type='game' THEN 'edition' ELSE r.scope_type END
 AND s.owner_key=CASE WHEN r.scope_type='game' THEN r.edition_id ELSE r.scope_key END
 AND s.driver_id=CASE WHEN r.driver_id='' THEN 'manual' ELSE r.driver_id END
GROUP BY s.id,r.edition_id;

INSERT INTO save_revisions(id,stream_id,device_id,parent_revision_id,content_hash,total_size,file_count,status,created_at)
SELECT r.id,s.id,r.device_id,
  CASE WHEN EXISTS(SELECT 1 FROM legacy_save_revisions p WHERE p.id=r.base_revision_id) THEN NULLIF(r.base_revision_id,'') ELSE NULL END,
  r.checksum,r.size,1,
  CASE WHEN r.conflict=1 THEN 'conflict'
    WHEN EXISTS(
      SELECT 1 FROM legacy_save_revisions newer
      WHERE newer.edition_id=r.edition_id AND newer.driver_id=r.driver_id AND newer.scope_type=r.scope_type AND newer.scope_key=r.scope_key
        AND newer.conflict=0 AND (newer.created_at>r.created_at OR (newer.created_at=r.created_at AND newer.id>r.id))
    ) THEN 'superseded' ELSE 'current' END,
  r.created_at
FROM legacy_save_revisions r
JOIN save_streams s ON s.owner_type=CASE WHEN r.scope_type='game' THEN 'edition' ELSE r.scope_type END
 AND s.owner_key=CASE WHEN r.scope_type='game' THEN r.edition_id ELSE r.scope_key END
 AND s.driver_id=CASE WHEN r.driver_id='' THEN 'manual' ELSE r.driver_id END;

INSERT INTO save_files(id,revision_id,logical_path,checksum,size,blob_path,mtime_ns,mode)
SELECT 'file-' || id,id,relative_path,checksum,size,blob_path,0,0 FROM legacy_save_revisions;
DROP TABLE legacy_save_revisions;

CREATE TABLE save_bindings (
  id TEXT PRIMARY KEY,
  stream_id TEXT NOT NULL REFERENCES save_streams(id) ON DELETE CASCADE,
  edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
  device_profile_id TEXT NOT NULL DEFAULT '',
  driver_id TEXT NOT NULL,
  local_paths_json TEXT NOT NULL DEFAULT '[]',
  discovery_json TEXT NOT NULL DEFAULT '{}',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(stream_id,edition_id,device_profile_id,driver_id)
);
CREATE TABLE pairing_codes (
  id TEXT PRIMARY KEY,
  code_hash TEXT NOT NULL UNIQUE,
  requested_device_json TEXT NOT NULL DEFAULT '{}',
  expires_at TEXT NOT NULL,
  redeemed_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE client_tokens (
  id TEXT PRIMARY KEY,
  device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  scopes_json TEXT NOT NULL DEFAULT '[]',
  issued_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_client_tokens_device ON client_tokens(device_id,revoked_at,expires_at);
CREATE TABLE sync_sessions (
  id TEXT PRIMARY KEY,
  device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE RESTRICT,
  idempotency_key TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('proposed','negotiating','transferring','verifying','complete','partial','aborted','failed')),
  base_manifest_hash TEXT NOT NULL DEFAULT '',
  operation_plan_hash TEXT NOT NULL DEFAULT '',
  uploaded_count INTEGER NOT NULL DEFAULT 0,
  downloaded_count INTEGER NOT NULL DEFAULT 0,
  conflict_count INTEGER NOT NULL DEFAULT 0,
  failure_code TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  finished_at TEXT NOT NULL DEFAULT '',
  UNIQUE(device_id,idempotency_key)
);
CREATE INDEX idx_sync_sessions_device ON sync_sessions(device_id,created_at DESC,id DESC);
CREATE TABLE sync_operations (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sync_sessions(id) ON DELETE CASCADE,
  stream_id TEXT NOT NULL REFERENCES save_streams(id) ON DELETE RESTRICT,
  action TEXT NOT NULL CHECK(action IN ('upload','download','conflict','noop')),
  status TEXT NOT NULL CHECK(status IN ('proposed','transferring','verified','complete','failed','skipped')),
  base_revision_id TEXT NOT NULL DEFAULT '',
  target_revision_id TEXT NOT NULL DEFAULT '',
  expected_hash TEXT NOT NULL DEFAULT '',
  actual_hash TEXT NOT NULL DEFAULT '',
  bytes INTEGER NOT NULL DEFAULT 0,
  failure_code TEXT NOT NULL DEFAULT '',
  detail_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX idx_sync_operations_session ON sync_operations(session_id,id);
CREATE TABLE inventory_items (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sync_sessions(id) ON DELETE CASCADE,
  client_item_id TEXT NOT NULL,
  platform_id TEXT NOT NULL,
  sha256 TEXT NOT NULL DEFAULT '',
  serial TEXT NOT NULL DEFAULT '',
  product_code TEXT NOT NULL DEFAULT '',
  title_id TEXT NOT NULL DEFAULT '',
  size INTEGER NOT NULL DEFAULT 0,
  match_status TEXT NOT NULL CHECK(match_status IN ('matched','ambiguous','unmatched')),
  matched_edition_id TEXT REFERENCES editions(id) ON DELETE SET NULL,
  match_method TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  UNIQUE(session_id,client_item_id)
);
`, `
CREATE TABLE IF NOT EXISTS runtime_import_hints (
  id TEXT PRIMARY KEY,
  edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
  source_kind TEXT NOT NULL CHECK(source_kind IN ('structured-sidecar','pegasus-command','esde-system')),
  source_format TEXT NOT NULL,
  device_profile_id TEXT NOT NULL DEFAULT '',
  frontend_adapter_id TEXT NOT NULL DEFAULT '',
  driver_id TEXT NOT NULL DEFAULT '',
  core_id TEXT NOT NULL DEFAULT '',
  arguments_json TEXT NOT NULL DEFAULT '[]',
  raw_command TEXT NOT NULL DEFAULT '',
  trust TEXT NOT NULL CHECK(trust IN ('structured','untrusted')),
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','applied','dismissed')),
  source_ref TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_runtime_import_hints_edition ON runtime_import_hints(edition_id,status,created_at,id);
`, `
DROP INDEX IF EXISTS idx_library_sources_identity;
CREATE UNIQUE INDEX IF NOT EXISTS idx_library_sources_identity ON library_sources(kind,root_path,metadata_path,runtime_metadata_path,platform);
`, `
SELECT 1;
`, `
DROP INDEX IF EXISTS idx_source_scans_source;
DROP INDEX IF EXISTS idx_library_sources_identity;
DROP INDEX IF EXISTS idx_library_sources_platform;
ALTER TABLE source_scans RENAME TO source_scans_v12;
ALTER TABLE library_sources RENAME TO library_sources_v12;
CREATE TABLE library_sources (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('rom_directory','pegasus','esde','varkiv')),
  root_path TEXT NOT NULL DEFAULT '',
  metadata_path TEXT NOT NULL DEFAULT '',
  runtime_metadata_path TEXT NOT NULL DEFAULT '',
  platform TEXT NOT NULL,
  metadata_locale TEXT NOT NULL DEFAULT '',
  rom_storage_policy TEXT NOT NULL CHECK(rom_storage_policy IN ('reference','copy')),
  media_storage_policy TEXT NOT NULL CHECK(media_storage_policy IN ('reference','copy','ignore')),
  enabled INTEGER NOT NULL DEFAULT 1,
  last_scan_at TEXT NOT NULL DEFAULT '',
  last_scan_status TEXT NOT NULL DEFAULT 'never',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
INSERT INTO library_sources(
  id,name,kind,root_path,metadata_path,runtime_metadata_path,platform,metadata_locale,
  rom_storage_policy,media_storage_policy,enabled,last_scan_at,last_scan_status,last_error,created_at,updated_at
)
SELECT
  id,name,kind,root_path,metadata_path,runtime_metadata_path,platform,metadata_locale,
  rom_storage_policy,media_storage_policy,enabled,last_scan_at,last_scan_status,last_error,created_at,updated_at
FROM library_sources_v12;
CREATE TABLE source_scans (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL REFERENCES library_sources(id) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK(status IN ('scanning','ready','committed','failed','stale')),
  requested_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL DEFAULT '',
  candidate_count INTEGER NOT NULL DEFAULT 0,
  importable_count INTEGER NOT NULL DEFAULT 0,
  missing_count INTEGER NOT NULL DEFAULT 0,
  duplicate_count INTEGER NOT NULL DEFAULT 0,
  conflict_count INTEGER NOT NULL DEFAULT 0,
  preview_token_hash TEXT NOT NULL DEFAULT '',
  failure_code TEXT NOT NULL DEFAULT '',
  failure_detail TEXT NOT NULL DEFAULT ''
);
INSERT INTO source_scans(
  id,source_id,status,requested_at,started_at,finished_at,expires_at,candidate_count,
  importable_count,missing_count,duplicate_count,conflict_count,preview_token_hash,failure_code,failure_detail
)
SELECT
  id,source_id,status,requested_at,started_at,finished_at,expires_at,candidate_count,
  importable_count,missing_count,duplicate_count,conflict_count,preview_token_hash,failure_code,failure_detail
FROM source_scans_v12;
DROP TABLE source_scans_v12;
DROP TABLE library_sources_v12;
CREATE INDEX idx_library_sources_platform ON library_sources(platform,kind,name);
CREATE UNIQUE INDEX idx_library_sources_identity ON library_sources(kind,root_path,metadata_path,runtime_metadata_path,platform);
CREATE INDEX idx_source_scans_source ON source_scans(source_id,requested_at DESC,id DESC);
`, `
CREATE TABLE IF NOT EXISTS source_adapters (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  format TEXT NOT NULL,
  handler TEXT NOT NULL CHECK(handler IN ('rom_directory','pegasus','esde','varkiv')),
  contract_version INTEGER NOT NULL DEFAULT 1,
  capabilities_json TEXT NOT NULL DEFAULT '{}',
  support_level TEXT NOT NULL CHECK(support_level IN ('catalogued','package-tested','hardware-tested','sync-tested')),
  evidence_json TEXT NOT NULL DEFAULT '{}',
  builtin INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_source_adapters_format ON source_adapters(format) WHERE enabled=1;
INSERT OR IGNORE INTO source_adapters(id,name,format,handler,contract_version,capabilities_json,support_level,evidence_json,builtin,enabled,created_at,updated_at) VALUES
  ('builtin-source-direct-rom','Direct ROM scanner','direct-rom','rom_directory',3,'{"files":true,"directories":true,"directory_platforms":true,"multi_disc":true,"preview":true,"managed_copy":true}','package-tested','{"scope":"fixture","verified_at":"2026-08-27","note":"Signed preview and atomic import tests cover files, CUE/BIN, M3U groups, and one top-level directory per game on directory-declared platforms."}',1,1,strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  ('builtin-source-pegasus','Pegasus metadata','pegasus','pegasus',2,'{"metadata":true,"media":true,"multi_file_games":true,"runtime_hints":true,"preview":true}','package-tested','{"scope":"fixture","verified_at":"2026-08-26","note":"Small Pegasus packages import, export, and reimport without losing Edition identity."}',1,1,strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  ('builtin-source-esde','ES-DE metadata','es-de','esde',2,'{"metadata":true,"media":true,"custom_systems":true,"runtime_hints":true,"preview":true}','package-tested','{"scope":"fixture","verified_at":"2026-08-26","note":"Small ES-DE packages import, export, and reimport with missing ROMs skipped."}',1,1,strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  ('builtin-source-varkiv','Neutral recovery manifest','varkiv','varkiv',4,'{"metadata":true,"series":true,"media":true,"runtime_hints":true,"stable_ids":true,"artifact_roles":true,"artifact_integrity":true,"neutral_manifest_v6":true,"portable_custom_platforms":true,"preview":true}','package-tested','{"scope":"fixture","verified_at":"2026-08-27","note":"Manifest v6 preserves v5 game and Artifact semantics and atomically restores referenced custom platform definitions; v4/v5 remain readable."}',1,1,strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now'));
UPDATE library_sources SET source_adapter_id=CASE kind
  WHEN 'rom_directory' THEN 'builtin-source-direct-rom'
  WHEN 'pegasus' THEN 'builtin-source-pegasus'
  WHEN 'esde' THEN 'builtin-source-esde'
  WHEN 'varkiv' THEN 'builtin-source-varkiv'
END;
CREATE INDEX IF NOT EXISTS idx_library_sources_adapter ON library_sources(source_adapter_id,enabled,name);
CREATE TRIGGER IF NOT EXISTS trg_library_sources_adapter_insert
BEFORE INSERT ON library_sources
WHEN NEW.source_adapter_id IS NULL OR NOT EXISTS (
  SELECT 1 FROM source_adapters WHERE id=NEW.source_adapter_id AND handler=NEW.kind
)
BEGIN SELECT RAISE(ABORT,'source adapter must match source kind'); END;
CREATE TRIGGER IF NOT EXISTS trg_library_sources_adapter_update
BEFORE UPDATE OF source_adapter_id,kind ON library_sources
WHEN NEW.source_adapter_id IS NULL OR NOT EXISTS (
  SELECT 1 FROM source_adapters WHERE id=NEW.source_adapter_id AND handler=NEW.kind
)
BEGIN SELECT RAISE(ABORT,'source adapter must match source kind'); END;
CREATE TRIGGER IF NOT EXISTS trg_source_adapters_handler_update
BEFORE UPDATE OF handler ON source_adapters
WHEN NEW.handler<>OLD.handler AND EXISTS (
  SELECT 1 FROM library_sources WHERE source_adapter_id=OLD.id
)
BEGIN SELECT RAISE(ABORT,'referenced source adapter handler cannot change'); END;
`, `
UPDATE artifacts SET role=lower(trim(role));
CREATE TRIGGER IF NOT EXISTS trg_artifacts_shape_insert
BEFORE INSERT ON artifacts
WHEN NEW.role NOT IN ('rom','disc','executable','patch','dlc','update','other')
  OR NEW.disc_index<0 OR NEW.disc_index>64 OR NEW.size<0 OR NEW.missing NOT IN (0,1)
BEGIN SELECT RAISE(ABORT,'invalid artifact shape'); END;
CREATE TRIGGER IF NOT EXISTS trg_artifacts_shape_update
BEFORE UPDATE OF role,disc_index,size,missing ON artifacts
WHEN NEW.role NOT IN ('rom','disc','executable','patch','dlc','update','other')
  OR NEW.disc_index<0 OR NEW.disc_index>64 OR NEW.size<0 OR NEW.missing NOT IN (0,1)
BEGIN SELECT RAISE(ABORT,'invalid artifact shape'); END;
`, `
DROP INDEX IF EXISTS idx_editions_work;
DROP INDEX IF EXISTS idx_media_work;
DROP INDEX IF EXISTS idx_series_members_work;
DROP INDEX IF EXISTS idx_series_members_order;
ALTER TABLE works RENAME TO games;
ALTER TABLE editions RENAME COLUMN work_id TO game_id;
ALTER TABLE media_assets RENAME COLUMN work_id TO game_id;
ALTER TABLE series_members RENAME COLUMN work_id TO game_id;
ALTER TABLE localized_titles RENAME TO localized_titles_v15;
CREATE TABLE localized_titles (
  owner_type TEXT NOT NULL CHECK(owner_type IN ('game','edition')),
  owner_id TEXT NOT NULL,
  locale TEXT NOT NULL,
  title TEXT NOT NULL,
  sort_title TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(owner_type, owner_id, locale)
);
INSERT INTO localized_titles(owner_type,owner_id,locale,title,sort_title)
SELECT CASE owner_type WHEN 'work' THEN 'game' ELSE owner_type END,owner_id,locale,title,sort_title
FROM localized_titles_v15;
DROP TABLE localized_titles_v15;
CREATE INDEX idx_editions_game ON editions(game_id, sort_order);
CREATE INDEX idx_media_game ON media_assets(game_id,kind,sort_order);
CREATE INDEX idx_series_members_game ON series_members(game_id);
CREATE INDEX idx_series_members_order ON series_members(series_id,sort_order,game_id);
`, `
CREATE TABLE IF NOT EXISTS custom_platforms (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  name_zh TEXT NOT NULL DEFAULT '',
  vendor TEXT NOT NULL DEFAULT 'Custom',
  category TEXT NOT NULL CHECK(category IN ('console','handheld','arcade','computer')),
  aliases_json TEXT NOT NULL DEFAULT '[]',
  extensions_json TEXT NOT NULL DEFAULT '[]',
  esde_systems_json TEXT NOT NULL DEFAULT '[]',
  bios TEXT NOT NULL CHECK(bios IN ('none','optional','required','varies')),
  runtime TEXT NOT NULL CHECK(runtime IN ('web','web_experimental','native')),
  suggested_emulators_json TEXT NOT NULL DEFAULT '{}',
  enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`, `
CREATE INDEX IF NOT EXISTS idx_media_content_status ON media_assets(content_status,storage_kind,id);
`, `
-- Hardware and sync claims created before v19 were checked only when they were
-- submitted. They were not bound to the exact runtime object and contract
-- version, so a later contract edit could leave stale claims visible. Preserve
-- the historical evidence, mark it stale, and lower the active support claim.
CREATE TABLE IF NOT EXISTS runtime_evidence_claims (
  runtime_kind TEXT NOT NULL CHECK(runtime_kind IN ('source_adapter','frontend_adapter','device_profile','emulator_driver','retroarch_core')),
  runtime_id TEXT NOT NULL,
  target TEXT NOT NULL,
  contract_version INTEGER NOT NULL CHECK(contract_version>0),
  support_level TEXT NOT NULL CHECK(support_level IN ('hardware-tested','sync-tested')),
  evidence_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(runtime_kind,runtime_id,target)
);
CREATE INDEX IF NOT EXISTS idx_runtime_evidence_claims_target ON runtime_evidence_claims(target,support_level,runtime_kind,runtime_id);
UPDATE source_adapters
SET support_level=CASE WHEN builtin=1 THEN 'package-tested' ELSE 'catalogued' END,
    evidence_json='{"stale":true,"stale_reason":"invalid_legacy_evidence"}'
WHERE support_level IN ('hardware-tested','sync-tested') AND NOT json_valid(evidence_json);
UPDATE source_adapters
SET support_level=CASE WHEN builtin=1 THEN 'package-tested' ELSE 'catalogued' END,
    evidence_json=json_set(evidence_json,'$.stale',json('true'),'$.stale_reason','runtime_contract_unbound')
WHERE support_level IN ('hardware-tested','sync-tested') AND (
  coalesce(json_type(evidence_json,'$.runtime_contract_version'),'')<>'integer'
  OR json_extract(evidence_json,'$.runtime_contract_version')<>contract_version
  OR coalesce(json_extract(evidence_json,'$.runtime_object_kind'),'')<>'source_adapter'
  OR coalesce(json_extract(evidence_json,'$.runtime_object_id'),'')<>id
);
UPDATE frontend_adapters
SET support_level=CASE WHEN builtin=1 THEN 'package-tested' ELSE 'catalogued' END,
    evidence_json='{"stale":true,"stale_reason":"invalid_legacy_evidence"}'
WHERE support_level IN ('hardware-tested','sync-tested') AND NOT json_valid(evidence_json);
UPDATE frontend_adapters
SET support_level=CASE WHEN builtin=1 THEN 'package-tested' ELSE 'catalogued' END,
    evidence_json=json_set(evidence_json,'$.stale',json('true'),'$.stale_reason','runtime_contract_unbound')
WHERE support_level IN ('hardware-tested','sync-tested') AND (
  coalesce(json_type(evidence_json,'$.runtime_contract_version'),'')<>'integer'
  OR json_extract(evidence_json,'$.runtime_contract_version')<>contract_version
  OR coalesce(json_extract(evidence_json,'$.runtime_object_kind'),'')<>'frontend_adapter'
  OR coalesce(json_extract(evidence_json,'$.runtime_object_id'),'')<>id
);
UPDATE device_profiles
SET support_level=CASE WHEN builtin=1 THEN 'package-tested' ELSE 'catalogued' END,
    evidence_json='{"stale":true,"stale_reason":"invalid_legacy_evidence"}'
WHERE support_level IN ('hardware-tested','sync-tested') AND NOT json_valid(evidence_json);
UPDATE device_profiles
SET support_level=CASE WHEN builtin=1 THEN 'package-tested' ELSE 'catalogued' END,
    evidence_json=json_set(evidence_json,'$.stale',json('true'),'$.stale_reason','runtime_contract_unbound')
WHERE support_level IN ('hardware-tested','sync-tested') AND (
  coalesce(json_type(evidence_json,'$.runtime_contract_version'),'')<>'integer'
  OR json_extract(evidence_json,'$.runtime_contract_version')<>contract_version
  OR coalesce(json_extract(evidence_json,'$.runtime_object_kind'),'')<>'device_profile'
  OR coalesce(json_extract(evidence_json,'$.runtime_object_id'),'')<>id
);
UPDATE emulator_drivers
SET support_level=CASE WHEN builtin=1 THEN 'package-tested' ELSE 'catalogued' END,
    evidence_json='{"stale":true,"stale_reason":"invalid_legacy_evidence"}'
WHERE support_level IN ('hardware-tested','sync-tested') AND NOT json_valid(evidence_json);
UPDATE emulator_drivers
SET support_level=CASE WHEN builtin=1 THEN 'package-tested' ELSE 'catalogued' END,
    evidence_json=json_set(evidence_json,'$.stale',json('true'),'$.stale_reason','runtime_contract_unbound')
WHERE support_level IN ('hardware-tested','sync-tested') AND (
  coalesce(json_type(evidence_json,'$.runtime_contract_version'),'')<>'integer'
  OR json_extract(evidence_json,'$.runtime_contract_version')<>contract_version
  OR coalesce(json_extract(evidence_json,'$.runtime_object_kind'),'')<>'emulator_driver'
  OR coalesce(json_extract(evidence_json,'$.runtime_object_id'),'')<>id
);
UPDATE retroarch_cores
SET support_level=CASE WHEN builtin=1 THEN 'package-tested' ELSE 'catalogued' END,
    evidence_json='{"stale":true,"stale_reason":"invalid_legacy_evidence"}'
WHERE support_level IN ('hardware-tested','sync-tested') AND NOT json_valid(evidence_json);
UPDATE retroarch_cores
SET support_level=CASE WHEN builtin=1 THEN 'package-tested' ELSE 'catalogued' END,
    evidence_json=json_set(evidence_json,'$.stale',json('true'),'$.stale_reason','runtime_contract_unbound')
WHERE support_level IN ('hardware-tested','sync-tested') AND (
  coalesce(json_type(evidence_json,'$.runtime_contract_version'),'')<>'integer'
  OR json_extract(evidence_json,'$.runtime_contract_version')<>contract_version
  OR coalesce(json_extract(evidence_json,'$.runtime_object_kind'),'')<>'retroarch_core'
  OR coalesce(json_extract(evidence_json,'$.runtime_object_id'),'')<>id
);
`, `
CREATE TABLE IF NOT EXISTS inventory_match_overrides (
  id TEXT PRIMARY KEY,
  device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  client_item_id TEXT NOT NULL,
  platform_id TEXT NOT NULL,
  identity_hash TEXT NOT NULL,
  candidate_hash TEXT NOT NULL,
  edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
  match_method TEXT NOT NULL CHECK(match_method IN ('sha256','serial','product_code','title_id')),
  source_session_id TEXT NOT NULL REFERENCES sync_sessions(id) ON DELETE RESTRICT,
  source_inventory_item_id TEXT NOT NULL REFERENCES inventory_items(id) ON DELETE RESTRICT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(device_id,client_item_id)
);
CREATE INDEX IF NOT EXISTS idx_inventory_match_overrides_edition ON inventory_match_overrides(edition_id,device_id);
`, `
UPDATE frontend_adapters
SET handler=CASE format WHEN 'pegasus' THEN 'pegasus' WHEN 'es-de' THEN 'es-de' ELSE handler END
WHERE handler='';
CREATE TRIGGER IF NOT EXISTS trg_frontend_adapters_handler_update
BEFORE UPDATE OF handler ON frontend_adapters
WHEN OLD.handler<>'' AND NEW.handler<>OLD.handler AND (
  EXISTS (SELECT 1 FROM device_profiles WHERE default_frontend_id=OLD.id)
  OR EXISTS (SELECT 1 FROM package_profiles WHERE frontend_adapter_id=OLD.id)
  OR EXISTS (SELECT 1 FROM launch_bindings WHERE frontend_adapter_id=OLD.id)
)
BEGIN SELECT RAISE(ABORT,'referenced frontend adapter handler cannot change'); END;
CREATE TRIGGER IF NOT EXISTS trg_package_profiles_frontend_insert
BEFORE INSERT ON package_profiles
WHEN NEW.frontend_adapter_id<>'' AND NOT EXISTS (
  SELECT 1 FROM frontend_adapters WHERE id=NEW.frontend_adapter_id AND enabled=1 AND handler=NEW.frontend
)
BEGIN SELECT RAISE(ABORT,'frontend adapter handler must match package frontend'); END;
CREATE TRIGGER IF NOT EXISTS trg_package_profiles_frontend_update
BEFORE UPDATE OF frontend_adapter_id,frontend ON package_profiles
WHEN NEW.frontend_adapter_id<>'' AND NOT EXISTS (
  SELECT 1 FROM frontend_adapters WHERE id=NEW.frontend_adapter_id AND enabled=1 AND handler=NEW.frontend
)
BEGIN SELECT RAISE(ABORT,'frontend adapter handler must match package frontend'); END;
`, `
CREATE INDEX IF NOT EXISTS idx_save_compatibility_members_runtime
ON save_compatibility_members(runtime_kind,driver_id,core_id,os_family,architecture);
CREATE INDEX IF NOT EXISTS idx_runtime_attestations_device
ON runtime_attestations(device_id,kind,runtime_id);
CREATE INDEX IF NOT EXISTS idx_save_streams_compatibility_group
ON save_streams(compatibility_group_id) WHERE compatibility_group_id IS NOT NULL;
CREATE TRIGGER IF NOT EXISTS trg_save_streams_compatibility_insert
BEFORE INSERT ON save_streams
WHEN NEW.compatibility_group_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM save_compatibility_groups g
  JOIN save_compatibility_members m ON m.group_id=g.id
  WHERE g.id=NEW.compatibility_group_id AND g.enabled=1
    AND m.driver_id=NEW.driver_id AND m.core_id='' AND m.runtime_kind='server'
)
BEGIN SELECT RAISE(ABORT,'save stream driver is not a trusted server member of the compatibility group'); END;
CREATE TRIGGER IF NOT EXISTS trg_save_streams_compatibility_update
BEFORE UPDATE OF compatibility_group_id,driver_id ON save_streams
WHEN NEW.compatibility_group_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM save_compatibility_groups g
  JOIN save_compatibility_members m ON m.group_id=g.id
  WHERE g.id=NEW.compatibility_group_id AND g.enabled=1
    AND m.driver_id=NEW.driver_id AND m.core_id='' AND m.runtime_kind='server'
)
BEGIN SELECT RAISE(ABORT,'save stream driver is not a trusted server member of the compatibility group'); END;
CREATE TRIGGER IF NOT EXISTS trg_save_bindings_compatibility_insert
BEFORE INSERT ON save_bindings
WHEN NEW.driver_id<>(SELECT driver_id FROM save_streams WHERE id=NEW.stream_id) AND NOT EXISTS (
  SELECT 1 FROM save_streams s
  JOIN save_compatibility_groups g ON g.id=s.compatibility_group_id AND g.enabled=1
  JOIN save_compatibility_members m ON m.group_id=g.id
  WHERE s.id=NEW.stream_id AND m.driver_id=NEW.driver_id
    AND m.core_id=COALESCE(NEW.core_id,'') AND m.runtime_kind='device'
)
BEGIN SELECT RAISE(ABORT,'cross-driver save binding is not a verified compatibility member'); END;
CREATE TRIGGER IF NOT EXISTS trg_save_bindings_compatibility_update
BEFORE UPDATE OF stream_id,driver_id,core_id ON save_bindings
WHEN NEW.driver_id<>(SELECT driver_id FROM save_streams WHERE id=NEW.stream_id) AND NOT EXISTS (
  SELECT 1 FROM save_streams s
  JOIN save_compatibility_groups g ON g.id=s.compatibility_group_id AND g.enabled=1
  JOIN save_compatibility_members m ON m.group_id=g.id
  WHERE s.id=NEW.stream_id AND m.driver_id=NEW.driver_id
    AND m.core_id=COALESCE(NEW.core_id,'') AND m.runtime_kind='device'
)
BEGIN SELECT RAISE(ABORT,'cross-driver save binding is not a verified compatibility member'); END;
`, `
UPDATE core_mappings SET builtin=1 WHERE lower(trim(id)) LIKE 'builtin-%';
CREATE TRIGGER IF NOT EXISTS trg_source_adapters_builtin_namespace_insert
BEFORE INSERT ON source_adapters
WHEN NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%'
BEGIN SELECT RAISE(ABORT,'builtin namespace is reserved'); END;
CREATE TRIGGER IF NOT EXISTS trg_frontend_adapters_builtin_namespace_insert
BEFORE INSERT ON frontend_adapters
WHEN NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%'
BEGIN SELECT RAISE(ABORT,'builtin namespace is reserved'); END;
CREATE TRIGGER IF NOT EXISTS trg_device_profiles_builtin_namespace_insert
BEFORE INSERT ON device_profiles
WHEN NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%'
BEGIN SELECT RAISE(ABORT,'builtin namespace is reserved'); END;
CREATE TRIGGER IF NOT EXISTS trg_emulator_drivers_builtin_namespace_insert
BEFORE INSERT ON emulator_drivers
WHEN NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%'
BEGIN SELECT RAISE(ABORT,'builtin namespace is reserved'); END;
CREATE TRIGGER IF NOT EXISTS trg_retroarch_cores_builtin_namespace_insert
BEFORE INSERT ON retroarch_cores
WHEN NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%'
BEGIN SELECT RAISE(ABORT,'builtin namespace is reserved'); END;
CREATE TRIGGER IF NOT EXISTS trg_core_mappings_builtin_namespace_insert
BEFORE INSERT ON core_mappings
WHEN NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%'
BEGIN SELECT RAISE(ABORT,'builtin namespace is reserved'); END;
`, `
UPDATE package_profiles SET builtin=1 WHERE lower(trim(id)) LIKE 'builtin-%';
CREATE TRIGGER IF NOT EXISTS trg_package_profiles_builtin_namespace_insert
BEFORE INSERT ON package_profiles
WHEN NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%'
BEGIN SELECT RAISE(ABORT,'builtin namespace is reserved'); END;
`, `
CREATE TRIGGER IF NOT EXISTS trg_source_adapters_builtin_ownership_update
BEFORE UPDATE OF id,builtin ON source_adapters
WHEN NEW.builtin<>OLD.builtin
  OR (OLD.builtin=1 AND NEW.id<>OLD.id)
  OR (NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%')
BEGIN SELECT RAISE(ABORT,'builtin ownership and namespace are immutable'); END;
CREATE TRIGGER IF NOT EXISTS trg_frontend_adapters_builtin_ownership_update
BEFORE UPDATE OF id,builtin ON frontend_adapters
WHEN NEW.builtin<>OLD.builtin
  OR (OLD.builtin=1 AND NEW.id<>OLD.id)
  OR (NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%')
BEGIN SELECT RAISE(ABORT,'builtin ownership and namespace are immutable'); END;
CREATE TRIGGER IF NOT EXISTS trg_device_profiles_builtin_ownership_update
BEFORE UPDATE OF id,builtin ON device_profiles
WHEN NEW.builtin<>OLD.builtin
  OR (OLD.builtin=1 AND NEW.id<>OLD.id)
  OR (NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%')
BEGIN SELECT RAISE(ABORT,'builtin ownership and namespace are immutable'); END;
CREATE TRIGGER IF NOT EXISTS trg_emulator_drivers_builtin_ownership_update
BEFORE UPDATE OF id,builtin ON emulator_drivers
WHEN NEW.builtin<>OLD.builtin
  OR (OLD.builtin=1 AND NEW.id<>OLD.id)
  OR (NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%')
BEGIN SELECT RAISE(ABORT,'builtin ownership and namespace are immutable'); END;
CREATE TRIGGER IF NOT EXISTS trg_retroarch_cores_builtin_ownership_update
BEFORE UPDATE OF id,builtin ON retroarch_cores
WHEN NEW.builtin<>OLD.builtin
  OR (OLD.builtin=1 AND NEW.id<>OLD.id)
  OR (NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%')
BEGIN SELECT RAISE(ABORT,'builtin ownership and namespace are immutable'); END;
CREATE TRIGGER IF NOT EXISTS trg_core_mappings_builtin_ownership_update
BEFORE UPDATE OF id,builtin ON core_mappings
WHEN NEW.builtin<>OLD.builtin
  OR (OLD.builtin=1 AND NEW.id<>OLD.id)
  OR (NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%')
BEGIN SELECT RAISE(ABORT,'builtin ownership and namespace are immutable'); END;
CREATE TRIGGER IF NOT EXISTS trg_package_profiles_builtin_ownership_update
BEFORE UPDATE OF id,builtin ON package_profiles
WHEN NEW.builtin<>OLD.builtin
  OR (OLD.builtin=1 AND NEW.id<>OLD.id)
  OR (NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%')
BEGIN SELECT RAISE(ABORT,'builtin ownership and namespace are immutable'); END;
`, `
DROP TRIGGER IF EXISTS trg_library_sources_adapter_insert;
DROP TRIGGER IF EXISTS trg_library_sources_adapter_update;
DROP TRIGGER IF EXISTS trg_source_adapters_handler_update;
DROP TRIGGER IF EXISTS trg_source_adapters_builtin_namespace_insert;
DROP TRIGGER IF EXISTS trg_source_adapters_builtin_ownership_update;
DROP INDEX IF EXISTS idx_source_scans_source;
DROP INDEX IF EXISTS idx_library_sources_adapter;
DROP INDEX IF EXISTS idx_library_sources_identity;
DROP INDEX IF EXISTS idx_library_sources_platform;
DROP INDEX IF EXISTS idx_source_adapters_format;
ALTER TABLE source_scans RENAME TO source_scans_v25;
ALTER TABLE library_sources RENAME TO library_sources_v25;
ALTER TABLE source_adapters RENAME TO source_adapters_v25;
CREATE TABLE source_adapters (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  format TEXT NOT NULL,
  handler TEXT NOT NULL CHECK(handler IN ('rom_directory','pegasus','esde','varkiv')),
  contract_version INTEGER NOT NULL DEFAULT 1,
  capabilities_json TEXT NOT NULL DEFAULT '{}',
  support_level TEXT NOT NULL CHECK(support_level IN ('catalogued','package-tested','hardware-tested','sync-tested')),
  evidence_json TEXT NOT NULL DEFAULT '{}',
  builtin INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
INSERT INTO source_adapters(
  id,name,format,handler,contract_version,capabilities_json,support_level,evidence_json,builtin,enabled,created_at,updated_at
)
SELECT
  CASE WHEN builtin=1 AND handler NOT IN ('rom_directory','pegasus','esde') THEN 'builtin-source-varkiv' ELSE id END,
  name,
  CASE WHEN builtin=1 AND handler NOT IN ('rom_directory','pegasus','esde') THEN 'varkiv' ELSE format END,
  CASE WHEN handler NOT IN ('rom_directory','pegasus','esde') THEN 'varkiv' ELSE handler END,
  contract_version,capabilities_json,support_level,evidence_json,builtin,enabled,created_at,updated_at
FROM source_adapters_v25;
CREATE TABLE library_sources (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('rom_directory','pegasus','esde','varkiv')),
  source_adapter_id TEXT REFERENCES source_adapters(id) ON DELETE RESTRICT,
  root_path TEXT NOT NULL DEFAULT '',
  metadata_path TEXT NOT NULL DEFAULT '',
  runtime_metadata_path TEXT NOT NULL DEFAULT '',
  platform TEXT NOT NULL,
  metadata_locale TEXT NOT NULL DEFAULT '',
  rom_storage_policy TEXT NOT NULL CHECK(rom_storage_policy IN ('reference','copy')),
  media_storage_policy TEXT NOT NULL CHECK(media_storage_policy IN ('reference','copy','ignore')),
  enabled INTEGER NOT NULL DEFAULT 1,
  last_scan_at TEXT NOT NULL DEFAULT '',
  last_scan_status TEXT NOT NULL DEFAULT 'never',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
INSERT INTO library_sources(
  id,name,kind,source_adapter_id,root_path,metadata_path,runtime_metadata_path,platform,metadata_locale,
  rom_storage_policy,media_storage_policy,enabled,last_scan_at,last_scan_status,last_error,created_at,updated_at
)
SELECT
  id,name,
  CASE WHEN kind NOT IN ('rom_directory','pegasus','esde') THEN 'varkiv' ELSE kind END,
  CASE WHEN source_adapter_id IN (
    SELECT id FROM source_adapters_v25 WHERE builtin=1 AND handler NOT IN ('rom_directory','pegasus','esde')
  ) THEN 'builtin-source-varkiv' ELSE source_adapter_id END,
  root_path,metadata_path,runtime_metadata_path,platform,metadata_locale,
  rom_storage_policy,media_storage_policy,enabled,last_scan_at,last_scan_status,last_error,created_at,updated_at
FROM library_sources_v25;
CREATE TABLE source_scans (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL REFERENCES library_sources(id) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK(status IN ('scanning','ready','committed','failed','stale')),
  requested_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL DEFAULT '',
  candidate_count INTEGER NOT NULL DEFAULT 0,
  importable_count INTEGER NOT NULL DEFAULT 0,
  missing_count INTEGER NOT NULL DEFAULT 0,
  duplicate_count INTEGER NOT NULL DEFAULT 0,
  conflict_count INTEGER NOT NULL DEFAULT 0,
  preview_token_hash TEXT NOT NULL DEFAULT '',
  failure_code TEXT NOT NULL DEFAULT '',
  failure_detail TEXT NOT NULL DEFAULT ''
);
INSERT INTO source_scans(
  id,source_id,status,requested_at,started_at,finished_at,expires_at,candidate_count,
  importable_count,missing_count,duplicate_count,conflict_count,preview_token_hash,failure_code,failure_detail
)
SELECT
  id,source_id,status,requested_at,started_at,finished_at,expires_at,candidate_count,
  importable_count,missing_count,duplicate_count,conflict_count,preview_token_hash,failure_code,failure_detail
FROM source_scans_v25;
DROP TABLE source_scans_v25;
DROP TABLE library_sources_v25;
DROP TABLE source_adapters_v25;
CREATE UNIQUE INDEX idx_source_adapters_format ON source_adapters(format) WHERE enabled=1;
CREATE INDEX idx_library_sources_platform ON library_sources(platform,kind,name);
CREATE UNIQUE INDEX idx_library_sources_identity ON library_sources(kind,root_path,metadata_path,runtime_metadata_path,platform);
CREATE INDEX idx_library_sources_adapter ON library_sources(source_adapter_id,enabled,name);
CREATE INDEX idx_source_scans_source ON source_scans(source_id,requested_at DESC,id DESC);
CREATE TRIGGER trg_library_sources_adapter_insert
BEFORE INSERT ON library_sources
WHEN NEW.source_adapter_id IS NULL OR NOT EXISTS (
  SELECT 1 FROM source_adapters WHERE id=NEW.source_adapter_id AND handler=NEW.kind
)
BEGIN SELECT RAISE(ABORT,'source adapter must match source kind'); END;
CREATE TRIGGER trg_library_sources_adapter_update
BEFORE UPDATE OF source_adapter_id,kind ON library_sources
WHEN NEW.source_adapter_id IS NULL OR NOT EXISTS (
  SELECT 1 FROM source_adapters WHERE id=NEW.source_adapter_id AND handler=NEW.kind
)
BEGIN SELECT RAISE(ABORT,'source adapter must match source kind'); END;
CREATE TRIGGER trg_source_adapters_handler_update
BEFORE UPDATE OF handler ON source_adapters
WHEN NEW.handler<>OLD.handler AND EXISTS (
  SELECT 1 FROM library_sources WHERE source_adapter_id=OLD.id
)
BEGIN SELECT RAISE(ABORT,'referenced source adapter handler cannot change'); END;
CREATE TRIGGER trg_source_adapters_builtin_namespace_insert
BEFORE INSERT ON source_adapters
WHEN NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%'
BEGIN SELECT RAISE(ABORT,'builtin namespace is reserved'); END;
CREATE TRIGGER trg_source_adapters_builtin_ownership_update
BEFORE UPDATE OF id,builtin ON source_adapters
WHEN NEW.builtin<>OLD.builtin
  OR (OLD.builtin=1 AND NEW.id<>OLD.id)
  OR (NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%')
BEGIN SELECT RAISE(ABORT,'builtin ownership and namespace are immutable'); END;
`, `
CREATE TABLE IF NOT EXISTS hash_sources (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  publisher TEXT NOT NULL DEFAULT '',
  license TEXT NOT NULL,
  trust_level TEXT NOT NULL DEFAULT 'imported' CHECK(trust_level IN ('local','imported','trusted')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS hash_releases (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL REFERENCES hash_sources(id) ON DELETE CASCADE,
  version TEXT NOT NULL,
  format_version INTEGER NOT NULL,
  pack_id TEXT NOT NULL CHECK(length(pack_id)=64 AND lower(pack_id)=pack_id),
  pack_sha256 TEXT NOT NULL CHECK(length(pack_sha256)=64 AND lower(pack_sha256)=pack_sha256),
  records_sha256 TEXT NOT NULL CHECK(length(records_sha256)=64 AND lower(records_sha256)=records_sha256),
  record_count INTEGER NOT NULL CHECK(record_count>0),
  source_name TEXT NOT NULL,
  publisher TEXT NOT NULL DEFAULT '',
  license TEXT NOT NULL,
  active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
  imported_at TEXT NOT NULL,
  UNIQUE(source_id,version),
  UNIQUE(source_id,pack_id),
  UNIQUE(source_id,pack_sha256)
);
CREATE TABLE IF NOT EXISTS hash_identities (
  id TEXT PRIMARY KEY,
  release_id TEXT NOT NULL REFERENCES hash_releases(id) ON DELETE CASCADE,
  source_id TEXT NOT NULL REFERENCES hash_sources(id) ON DELETE CASCADE,
  sha256 TEXT NOT NULL CHECK(length(sha256)=64 AND lower(sha256)=sha256),
  size INTEGER NOT NULL CHECK(size>0),
  crc32 TEXT NOT NULL DEFAULT '',
  md5 TEXT NOT NULL DEFAULT '',
  platform TEXT NOT NULL,
  game_key TEXT NOT NULL,
  game_default_title TEXT NOT NULL,
  game_titles_json TEXT NOT NULL DEFAULT '{}',
  edition_default_title TEXT NOT NULL,
  edition_titles_json TEXT NOT NULL DEFAULT '{}',
  edition_type TEXT NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  languages_json TEXT NOT NULL DEFAULT '[]',
  author TEXT NOT NULL DEFAULT '',
  region TEXT NOT NULL DEFAULT '',
  serial TEXT NOT NULL DEFAULT '',
  product_code TEXT NOT NULL DEFAULT '',
  title_id TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL CHECK(role IN ('rom','disc','executable')),
  disc_index INTEGER NOT NULL DEFAULT 0 CHECK(disc_index BETWEEN 0 AND 64),
  parent_sha256 TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  UNIQUE(release_id,sha256)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_hash_releases_active ON hash_releases(source_id) WHERE active=1;
CREATE INDEX IF NOT EXISTS idx_hash_releases_source ON hash_releases(source_id,imported_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_hash_identities_sha ON hash_identities(sha256,source_id);
CREATE INDEX IF NOT EXISTS idx_hash_identities_platform ON hash_identities(platform,game_default_title,sha256);
CREATE INDEX IF NOT EXISTS idx_hash_identities_release ON hash_identities(release_id,sha256);
`, `
DROP TRIGGER IF EXISTS trg_library_sources_adapter_insert;
DROP TRIGGER IF EXISTS trg_library_sources_adapter_update;
DROP TRIGGER IF EXISTS trg_source_adapters_handler_update;
DROP TRIGGER IF EXISTS trg_source_adapters_builtin_namespace_insert;
DROP TRIGGER IF EXISTS trg_source_adapters_builtin_ownership_update;
DROP INDEX IF EXISTS idx_source_scans_source;
DROP INDEX IF EXISTS idx_library_sources_adapter;
DROP INDEX IF EXISTS idx_library_sources_identity;
DROP INDEX IF EXISTS idx_library_sources_platform;
DROP INDEX IF EXISTS idx_source_adapters_format;
ALTER TABLE source_scans RENAME TO source_scans_v27;
ALTER TABLE library_sources RENAME TO library_sources_v27;
ALTER TABLE source_adapters RENAME TO source_adapters_v27;
CREATE TABLE source_adapters (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  format TEXT NOT NULL,
  handler TEXT NOT NULL CHECK(handler IN ('rom_directory','pegasus','esde','varkiv')),
  contract_version INTEGER NOT NULL DEFAULT 1,
  capabilities_json TEXT NOT NULL DEFAULT '{}',
  support_level TEXT NOT NULL CHECK(support_level IN ('catalogued','package-tested','hardware-tested','sync-tested')),
  evidence_json TEXT NOT NULL DEFAULT '{}',
  builtin INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
INSERT INTO source_adapters(
  id,name,format,handler,contract_version,capabilities_json,support_level,evidence_json,builtin,enabled,created_at,updated_at
)
SELECT
  CASE WHEN builtin=1 AND handler NOT IN ('rom_directory','pegasus','esde') THEN 'builtin-source-varkiv' ELSE id END,
  name,
  CASE WHEN builtin=1 AND handler NOT IN ('rom_directory','pegasus','esde') THEN 'varkiv' ELSE format END,
  CASE WHEN handler NOT IN ('rom_directory','pegasus','esde') THEN 'varkiv' ELSE handler END,
  contract_version,capabilities_json,support_level,evidence_json,builtin,enabled,created_at,updated_at
FROM source_adapters_v27;
CREATE TABLE library_sources (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('rom_directory','pegasus','esde','varkiv')),
  source_adapter_id TEXT REFERENCES source_adapters(id) ON DELETE RESTRICT,
  root_path TEXT NOT NULL DEFAULT '',
  metadata_path TEXT NOT NULL DEFAULT '',
  runtime_metadata_path TEXT NOT NULL DEFAULT '',
  platform TEXT NOT NULL,
  metadata_locale TEXT NOT NULL DEFAULT '',
  rom_storage_policy TEXT NOT NULL CHECK(rom_storage_policy IN ('reference','copy')),
  media_storage_policy TEXT NOT NULL CHECK(media_storage_policy IN ('reference','copy','ignore')),
  enabled INTEGER NOT NULL DEFAULT 1,
  last_scan_at TEXT NOT NULL DEFAULT '',
  last_scan_status TEXT NOT NULL DEFAULT 'never',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
INSERT INTO library_sources(
  id,name,kind,source_adapter_id,root_path,metadata_path,runtime_metadata_path,platform,metadata_locale,
  rom_storage_policy,media_storage_policy,enabled,last_scan_at,last_scan_status,last_error,created_at,updated_at
)
SELECT
  id,name,
  CASE WHEN kind NOT IN ('rom_directory','pegasus','esde') THEN 'varkiv' ELSE kind END,
  CASE WHEN source_adapter_id IN (
    SELECT id FROM source_adapters_v27 WHERE builtin=1 AND handler NOT IN ('rom_directory','pegasus','esde')
  ) THEN 'builtin-source-varkiv' ELSE source_adapter_id END,
  root_path,metadata_path,runtime_metadata_path,platform,metadata_locale,
  rom_storage_policy,media_storage_policy,enabled,last_scan_at,last_scan_status,last_error,created_at,updated_at
FROM library_sources_v27;
CREATE TABLE source_scans (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL REFERENCES library_sources(id) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK(status IN ('scanning','ready','committed','failed','stale')),
  requested_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL DEFAULT '',
  candidate_count INTEGER NOT NULL DEFAULT 0,
  importable_count INTEGER NOT NULL DEFAULT 0,
  missing_count INTEGER NOT NULL DEFAULT 0,
  duplicate_count INTEGER NOT NULL DEFAULT 0,
  conflict_count INTEGER NOT NULL DEFAULT 0,
  preview_token_hash TEXT NOT NULL DEFAULT '',
  failure_code TEXT NOT NULL DEFAULT '',
  failure_detail TEXT NOT NULL DEFAULT ''
);
INSERT INTO source_scans(
  id,source_id,status,requested_at,started_at,finished_at,expires_at,candidate_count,
  importable_count,missing_count,duplicate_count,conflict_count,preview_token_hash,failure_code,failure_detail
)
SELECT
  id,source_id,status,requested_at,started_at,finished_at,expires_at,candidate_count,
  importable_count,missing_count,duplicate_count,conflict_count,preview_token_hash,failure_code,failure_detail
FROM source_scans_v27;
DROP TABLE source_scans_v27;
DROP TABLE library_sources_v27;
DROP TABLE source_adapters_v27;
CREATE UNIQUE INDEX idx_source_adapters_format ON source_adapters(format) WHERE enabled=1;
CREATE INDEX idx_library_sources_platform ON library_sources(platform,kind,name);
CREATE UNIQUE INDEX idx_library_sources_identity ON library_sources(kind,root_path,metadata_path,runtime_metadata_path,platform);
CREATE INDEX idx_library_sources_adapter ON library_sources(source_adapter_id,enabled,name);
CREATE INDEX idx_source_scans_source ON source_scans(source_id,requested_at DESC,id DESC);
CREATE TRIGGER trg_library_sources_adapter_insert
BEFORE INSERT ON library_sources
WHEN NEW.source_adapter_id IS NULL OR NOT EXISTS (
  SELECT 1 FROM source_adapters WHERE id=NEW.source_adapter_id AND handler=NEW.kind
)
BEGIN SELECT RAISE(ABORT,'source adapter must match source kind'); END;
CREATE TRIGGER trg_library_sources_adapter_update
BEFORE UPDATE OF source_adapter_id,kind ON library_sources
WHEN NEW.source_adapter_id IS NULL OR NOT EXISTS (
  SELECT 1 FROM source_adapters WHERE id=NEW.source_adapter_id AND handler=NEW.kind
)
BEGIN SELECT RAISE(ABORT,'source adapter must match source kind'); END;
CREATE TRIGGER trg_source_adapters_handler_update
BEFORE UPDATE OF handler ON source_adapters
WHEN NEW.handler<>OLD.handler AND EXISTS (
  SELECT 1 FROM library_sources WHERE source_adapter_id=OLD.id
)
BEGIN SELECT RAISE(ABORT,'referenced source adapter handler cannot change'); END;
CREATE TRIGGER trg_source_adapters_builtin_namespace_insert
BEFORE INSERT ON source_adapters
WHEN NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%'
BEGIN SELECT RAISE(ABORT,'builtin namespace is reserved'); END;
CREATE TRIGGER trg_source_adapters_builtin_ownership_update
BEFORE UPDATE OF id,builtin ON source_adapters
WHEN NEW.builtin<>OLD.builtin
  OR (OLD.builtin=1 AND NEW.id<>OLD.id)
  OR (NEW.builtin=0 AND lower(trim(NEW.id)) LIKE 'builtin-%')
BEGIN SELECT RAISE(ABORT,'builtin ownership and namespace are immutable'); END;
`}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > CurrentSchemaVersion {
		return fmt.Errorf("database schema %d is newer than supported version %d", version, CurrentSchemaVersion)
	}
	// Drop known newer triggers before any table rename. A deliberately
	// downgraded fixture may already have removed one of the tables referenced
	// by those triggers, and SQLite validates trigger SQL during ALTER TABLE.
	if version < 14 {
		if _, err = tx.Exec(`
			DROP TRIGGER IF EXISTS trg_library_sources_adapter_insert;
			DROP TRIGGER IF EXISTS trg_library_sources_adapter_update;
			DROP TRIGGER IF EXISTS trg_source_adapters_handler_update;
		`); err != nil {
			return fmt.Errorf("prepare database migration triggers: %w", err)
		}
	}
	if version < 15 {
		if _, err = tx.Exec(`
			DROP TRIGGER IF EXISTS trg_artifacts_shape_insert;
			DROP TRIGGER IF EXISTS trg_artifacts_shape_update;
		`); err != nil {
			return fmt.Errorf("prepare database migration triggers: %w", err)
		}
	}
	// Migration tests and recovery drills can intentionally lower user_version
	// while retaining the newer physical names. Rewind only the v16 naming
	// layer inside this same transaction so the historical migrations still see
	// their original v1-v15 schema, then let v16 run again normally. A mixed
	// database containing both names is rejected rather than guessed at.
	if version < 16 {
		var hasGames int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='games'`).Scan(&hasGames); err != nil {
			return fmt.Errorf("inspect database migration 16 state: %w", err)
		}
		if hasGames != 0 {
			if _, err = tx.Exec(`
				DROP INDEX IF EXISTS idx_editions_game;
				DROP INDEX IF EXISTS idx_media_game;
				DROP INDEX IF EXISTS idx_series_members_game;
				DROP INDEX IF EXISTS idx_series_members_order;
				ALTER TABLE games RENAME TO works;
				ALTER TABLE editions RENAME COLUMN game_id TO work_id;
				ALTER TABLE media_assets RENAME COLUMN game_id TO work_id;
				ALTER TABLE series_members RENAME COLUMN game_id TO work_id;
				ALTER TABLE localized_titles RENAME TO localized_titles_v16;
				CREATE TABLE localized_titles (
				  owner_type TEXT NOT NULL CHECK(owner_type IN ('work','edition')),
				  owner_id TEXT NOT NULL,
				  locale TEXT NOT NULL,
				  title TEXT NOT NULL,
				  sort_title TEXT NOT NULL DEFAULT '',
				  PRIMARY KEY(owner_type, owner_id, locale)
				);
				INSERT INTO localized_titles(owner_type,owner_id,locale,title,sort_title)
				SELECT CASE owner_type WHEN 'game' THEN 'work' ELSE owner_type END,owner_id,locale,title,sort_title
				FROM localized_titles_v16;
				DROP TABLE localized_titles_v16;
				CREATE INDEX idx_editions_work ON editions(work_id, sort_order);
				CREATE INDEX idx_media_work ON media_assets(work_id,kind,sort_order);
				CREATE INDEX idx_series_members_work ON series_members(work_id);
				CREATE INDEX idx_series_members_order ON series_members(series_id,sort_order,work_id);
			`); err != nil {
				return fmt.Errorf("prepare database migration 16 naming state: %w", err)
			}
		}
	}
	// A restored or downgrade-test database can retain newer trigger objects
	// while advertising an older user_version. Remove only known guards before
	// older table rebuilds; their owning migrations recreate them.
	if version < 14 {
		if _, err = tx.Exec(`
			DROP TRIGGER IF EXISTS trg_library_sources_adapter_insert;
			DROP TRIGGER IF EXISTS trg_library_sources_adapter_update;
			DROP TRIGGER IF EXISTS trg_source_adapters_handler_update;
		`); err != nil {
			return fmt.Errorf("prepare database migration triggers: %w", err)
		}
	}
	if version < 15 {
		if _, err = tx.Exec(`
			DROP TRIGGER IF EXISTS trg_artifacts_shape_insert;
			DROP TRIGGER IF EXISTS trg_artifacts_shape_update;
		`); err != nil {
			return fmt.Errorf("prepare database migration triggers: %w", err)
		}
	}
	for index := version; index < len(migrations); index++ {
		// Schema v5 makes a non-empty ROM hash globally unique. Fail with an
		// actionable error before SQLite attempts to build the index so an
		// operator can resolve legacy duplicates while user_version remains 4.
		if index == 4 {
			var hash string
			var count int
			err = tx.QueryRow(`SELECT sha256, COUNT(*) FROM artifacts WHERE sha256<>'' GROUP BY sha256 HAVING COUNT(*)>1 ORDER BY sha256 LIMIT 1`).Scan(&hash, &count)
			switch {
			case err == nil:
				return fmt.Errorf("database migration 5 cannot create unique ROM identity: duplicate artifact SHA-256 %q appears %d times; resolve the duplicates on a backup before retrying", hash, count)
			case !errors.Is(err, sql.ErrNoRows):
				return fmt.Errorf("preflight database migration 5: %w", err)
			}
		}
		if index == 14 {
			var id, role string
			var discIndex int
			var size int64
			var missing int
			err = tx.QueryRow(`SELECT id,role,disc_index,size,missing FROM artifacts
				WHERE lower(trim(role)) NOT IN ('rom','disc','executable','patch','dlc','update','other')
				   OR disc_index<0 OR disc_index>64 OR size<0 OR missing NOT IN (0,1)
				ORDER BY id LIMIT 1`).Scan(&id, &role, &discIndex, &size, &missing)
			switch {
			case err == nil:
				return fmt.Errorf("database migration 15 cannot constrain invalid artifact %q; role=%q disc_index=%d size=%d missing=%d", id, role, discIndex, size, missing)
			case !errors.Is(err, sql.ErrNoRows):
				return fmt.Errorf("preflight database migration 15: %w", err)
			}
		}
		// SQLite has no portable ADD COLUMN IF NOT EXISTS. Test fixtures can
		// deliberately lower user_version while retaining newer columns, so
		// inspect the table before applying the v11 index migration.
		if index == 10 {
			rows, queryErr := tx.Query(`PRAGMA table_info(library_sources)`)
			if queryErr != nil {
				return queryErr
			}
			hasRuntimePath := false
			for rows.Next() {
				var cid, notNull, primaryKey int
				var name, columnType string
				var defaultValue any
				if queryErr = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); queryErr != nil {
					rows.Close()
					return queryErr
				}
				if name == "runtime_metadata_path" {
					hasRuntimePath = true
				}
			}
			if queryErr = rows.Close(); queryErr != nil {
				return queryErr
			}
			if !hasRuntimePath {
				if _, queryErr = tx.Exec(`ALTER TABLE library_sources ADD COLUMN runtime_metadata_path TEXT NOT NULL DEFAULT ''`); queryErr != nil {
					return fmt.Errorf("prepare database migration 11: %w", queryErr)
				}
			}
		}
		// v12 adds versioned contracts to curated device/core entries. Some
		// migration fixtures deliberately lower user_version while retaining
		// later columns, so add each column only when table_info proves it is
		// absent.
		if index == 11 {
			hasColumn := func(table, column string) (bool, error) {
				rows, queryErr := tx.Query(`PRAGMA table_info(` + table + `)`)
				if queryErr != nil {
					return false, queryErr
				}
				defer rows.Close()
				for rows.Next() {
					var cid, notNull, primaryKey int
					var name, columnType string
					var defaultValue any
					if queryErr = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); queryErr != nil {
						return false, queryErr
					}
					if name == column {
						return true, nil
					}
				}
				return false, rows.Err()
			}
			for _, table := range []string{"device_profiles", "retroarch_cores"} {
				hasVersion, queryErr := hasColumn(table, "contract_version")
				if queryErr != nil {
					return fmt.Errorf("prepare database migration 12: %w", queryErr)
				}
				if !hasVersion {
					if _, queryErr = tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN contract_version INTEGER NOT NULL DEFAULT 1`); queryErr != nil {
						return fmt.Errorf("prepare database migration 12: %w", queryErr)
					}
				}
			}
		}
		// v14 introduces the SourceAdapter registry. Migration fixtures can
		// lower user_version while retaining the newly added reference column,
		// so add it only when the current table genuinely lacks it.
		if index == 13 {
			rows, queryErr := tx.Query(`PRAGMA table_info(library_sources)`)
			if queryErr != nil {
				return queryErr
			}
			hasAdapterID := false
			for rows.Next() {
				var cid, notNull, primaryKey int
				var name, columnType string
				var defaultValue any
				if queryErr = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); queryErr != nil {
					rows.Close()
					return queryErr
				}
				if name == "source_adapter_id" {
					hasAdapterID = true
				}
			}
			if queryErr = rows.Close(); queryErr != nil {
				return queryErr
			}
			if !hasAdapterID {
				if _, queryErr = tx.Exec(`ALTER TABLE library_sources ADD COLUMN source_adapter_id TEXT REFERENCES source_adapters(id) ON DELETE RESTRICT`); queryErr != nil {
					return fmt.Errorf("prepare database migration 14: %w", queryErr)
				}
			}
		}
		// v18 records only the last explicit media availability check. Migration
		// must not read library/NAS/media bytes, so existing rows start unverified.
		// Downgrade/recovery fixtures may retain the column with user_version 17.
		if index == 17 {
			rows, queryErr := tx.Query(`PRAGMA table_info(media_assets)`)
			if queryErr != nil {
				return queryErr
			}
			hasContentStatus, hasContentCheckedAt := false, false
			for rows.Next() {
				var cid, notNull, primaryKey int
				var name, columnType string
				var defaultValue any
				if queryErr = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); queryErr != nil {
					rows.Close()
					return queryErr
				}
				if name == "content_status" {
					hasContentStatus = true
				}
				if name == "content_checked_at" {
					hasContentCheckedAt = true
				}
			}
			if queryErr = rows.Close(); queryErr != nil {
				return queryErr
			}
			if !hasContentStatus {
				if _, queryErr = tx.Exec(`ALTER TABLE media_assets ADD COLUMN content_status TEXT NOT NULL DEFAULT 'unverified' CHECK(content_status IN ('unverified','available','missing','changed','unsafe'))`); queryErr != nil {
					return fmt.Errorf("prepare database migration 18: %w", queryErr)
				}
			}
			if !hasContentCheckedAt {
				if _, queryErr = tx.Exec(`ALTER TABLE media_assets ADD COLUMN content_checked_at TEXT NOT NULL DEFAULT ''`); queryErr != nil {
					return fmt.Errorf("prepare database migration 18 checked time: %w", queryErr)
				}
			}
			var invalidID, invalidStatus string
			queryErr = tx.QueryRow(`SELECT id,content_status FROM media_assets WHERE content_status NOT IN ('unverified','available','missing','changed','unsafe') ORDER BY id LIMIT 1`).Scan(&invalidID, &invalidStatus)
			switch {
			case queryErr == nil:
				return fmt.Errorf("database migration 18 cannot constrain media %q with invalid content_status %q", invalidID, invalidStatus)
			case !errors.Is(queryErr, sql.ErrNoRows):
				return fmt.Errorf("preflight database migration 18: %w", queryErr)
			}
		}
		// v21 gives FrontendAdapter the same safe compiled-handler boundary as
		// SourceAdapter. Databases deliberately rewound for migration tests may
		// retain the additive column, so inspect it before ALTER TABLE.
		if index == 20 {
			rows, queryErr := tx.Query(`PRAGMA table_info(frontend_adapters)`)
			if queryErr != nil {
				return queryErr
			}
			hasHandler := false
			for rows.Next() {
				var cid, notNull, primaryKey int
				var name, columnType string
				var defaultValue any
				if queryErr = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); queryErr != nil {
					rows.Close()
					return queryErr
				}
				if name == "handler" {
					hasHandler = true
				}
			}
			if queryErr = rows.Close(); queryErr != nil {
				return queryErr
			}
			if !hasHandler {
				if _, queryErr = tx.Exec(`ALTER TABLE frontend_adapters ADD COLUMN handler TEXT NOT NULL DEFAULT '' CHECK(handler IN ('','pegasus','es-de'))`); queryErr != nil {
					return fmt.Errorf("prepare database migration 21: %w", queryErr)
				}
			}
		}
		// v22 introduces exact runtime attestations and versioned save-format
		// compatibility groups. The tables must exist before nullable foreign-key
		// columns are added. Rewound migration fixtures may retain either column,
		// so inspect both instead of guessing or rebuilding save history.
		if index == 21 {
			if _, queryErr := tx.Exec(`
				CREATE TABLE IF NOT EXISTS save_compatibility_groups (
				  id TEXT PRIMARY KEY,
				  name TEXT NOT NULL,
				  format TEXT NOT NULL,
				  contract_version INTEGER NOT NULL,
				  evidence_json TEXT NOT NULL DEFAULT '{}',
				  builtin INTEGER NOT NULL DEFAULT 0,
				  enabled INTEGER NOT NULL DEFAULT 1,
				  created_at TEXT NOT NULL,
				  updated_at TEXT NOT NULL
				);
				CREATE TABLE IF NOT EXISTS save_compatibility_members (
				  group_id TEXT NOT NULL REFERENCES save_compatibility_groups(id) ON DELETE CASCADE,
				  driver_id TEXT NOT NULL REFERENCES emulator_drivers(id) ON DELETE RESTRICT,
				  core_id TEXT NOT NULL DEFAULT '',
				  runtime_kind TEXT NOT NULL CHECK(runtime_kind IN ('server','device')),
				  driver_contract_version INTEGER NOT NULL,
				  core_contract_version INTEGER NOT NULL DEFAULT 0,
				  os_family TEXT NOT NULL DEFAULT '',
				  architecture TEXT NOT NULL DEFAULT '',
				  driver_sha256 TEXT NOT NULL DEFAULT '',
				  driver_size INTEGER NOT NULL DEFAULT 0,
				  core_sha256 TEXT NOT NULL DEFAULT '',
				  core_size INTEGER NOT NULL DEFAULT 0,
				  PRIMARY KEY(group_id,driver_id,core_id,os_family,architecture),
				  CHECK((core_id='' AND core_contract_version=0 AND core_sha256='' AND core_size=0) OR
				        (core_id<>'' AND core_contract_version>0 AND length(core_sha256)=64 AND core_size>0)),
				  CHECK((runtime_kind='server' AND os_family='' AND architecture='' AND driver_sha256='' AND driver_size=0) OR
				        (runtime_kind='device' AND os_family<>'' AND architecture<>'' AND length(driver_sha256)=64 AND driver_size>0))
				);
				CREATE TABLE IF NOT EXISTS runtime_attestations (
				  device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
				  kind TEXT NOT NULL CHECK(kind IN ('driver','core')),
				  runtime_id TEXT NOT NULL,
				  contract_version INTEGER NOT NULL,
				  sha256 TEXT NOT NULL CHECK(length(sha256)=64 AND lower(sha256)=sha256),
				  size INTEGER NOT NULL CHECK(size>0),
				  observed_at TEXT NOT NULL,
				  PRIMARY KEY(device_id,kind,runtime_id)
				);
			`); queryErr != nil {
				return fmt.Errorf("prepare database migration 22 tables: %w", queryErr)
			}
			hasColumn := func(table, column string) (bool, error) {
				rows, queryErr := tx.Query(`PRAGMA table_info(` + table + `)`)
				if queryErr != nil {
					return false, queryErr
				}
				defer rows.Close()
				for rows.Next() {
					var cid, notNull, primaryKey int
					var name, columnType string
					var defaultValue any
					if queryErr = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); queryErr != nil {
						return false, queryErr
					}
					if name == column {
						return true, nil
					}
				}
				return false, rows.Err()
			}
			hasGroup, queryErr := hasColumn("save_streams", "compatibility_group_id")
			if queryErr != nil {
				return fmt.Errorf("prepare database migration 22 stream column: %w", queryErr)
			}
			if !hasGroup {
				if _, queryErr = tx.Exec(`ALTER TABLE save_streams ADD COLUMN compatibility_group_id TEXT REFERENCES save_compatibility_groups(id) ON DELETE RESTRICT`); queryErr != nil {
					return fmt.Errorf("prepare database migration 22 stream group: %w", queryErr)
				}
			}
			hasCore, queryErr := hasColumn("save_bindings", "core_id")
			if queryErr != nil {
				return fmt.Errorf("prepare database migration 22 binding column: %w", queryErr)
			}
			if !hasCore {
				if _, queryErr = tx.Exec(`ALTER TABLE save_bindings ADD COLUMN core_id TEXT REFERENCES retroarch_cores(id) ON DELETE RESTRICT`); queryErr != nil {
					return fmt.Errorf("prepare database migration 22 binding core: %w", queryErr)
				}
			}
		}
		// v23 records ownership for curated core mappings and reserves the
		// builtin-* namespace before future catalog additions can collide with
		// user-created definitions. Rewound fixtures may retain the additive
		// column, so inspect it before ALTER TABLE.
		if index == 22 {
			rows, queryErr := tx.Query(`PRAGMA table_info(core_mappings)`)
			if queryErr != nil {
				return queryErr
			}
			hasBuiltin := false
			for rows.Next() {
				var cid, notNull, primaryKey int
				var name, columnType string
				var defaultValue any
				if queryErr = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); queryErr != nil {
					rows.Close()
					return queryErr
				}
				if name == "builtin" {
					hasBuiltin = true
				}
			}
			if queryErr = rows.Close(); queryErr != nil {
				return queryErr
			}
			if !hasBuiltin {
				if _, queryErr = tx.Exec(`ALTER TABLE core_mappings ADD COLUMN builtin INTEGER NOT NULL DEFAULT 0 CHECK(builtin IN (0,1))`); queryErr != nil {
					return fmt.Errorf("prepare database migration 23: %w", queryErr)
				}
			}
		}
		// v24 gives seeded PackageProfiles the same explicit ownership and
		// immutable API contract as the rest of the built-in runtime catalog.
		// A rewound additive fixture may already retain the column.
		if index == 23 {
			rows, queryErr := tx.Query(`PRAGMA table_info(package_profiles)`)
			if queryErr != nil {
				return queryErr
			}
			hasBuiltin := false
			for rows.Next() {
				var cid, notNull, primaryKey int
				var name, columnType string
				var defaultValue any
				if queryErr = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); queryErr != nil {
					rows.Close()
					return queryErr
				}
				if name == "builtin" {
					hasBuiltin = true
				}
			}
			if queryErr = rows.Close(); queryErr != nil {
				return queryErr
			}
			if !hasBuiltin {
				if _, queryErr = tx.Exec(`ALTER TABLE package_profiles ADD COLUMN builtin INTEGER NOT NULL DEFAULT 0 CHECK(builtin IN (0,1))`); queryErr != nil {
					return fmt.Errorf("prepare database migration 24: %w", queryErr)
				}
			}
		}
		// v25 makes the application-owned bit and identifier namespace a
		// database invariant. Refuse to bless a v24 database that was already
		// tampered with: silently promoting such a row would turn user-controlled
		// content into an application-owned definition during an upgrade.
		if index == 24 {
			for _, table := range []string{"source_adapters", "frontend_adapters", "device_profiles", "emulator_drivers", "retroarch_cores", "core_mappings", "package_profiles"} {
				var id string
				queryErr := tx.QueryRow(`SELECT id FROM ` + table + ` WHERE builtin=0 AND lower(trim(id)) LIKE 'builtin-%' LIMIT 1`).Scan(&id)
				if queryErr == nil {
					return fmt.Errorf("prepare database migration 25: %s contains reserved identifier %q without application ownership", table, id)
				}
				if !errors.Is(queryErr, sql.ErrNoRows) {
					return fmt.Errorf("prepare database migration 25 ownership check for %s: %w", table, queryErr)
				}
			}
		}
		if _, err = tx.Exec(migrations[index]); err != nil {
			return fmt.Errorf("apply database migration %d: %w", index+1, err)
		}
		if _, err = tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, index+1)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
