CREATE TABLE offline_epoch(id INTEGER PRIMARY KEY CHECK(id=1),epoch TEXT NOT NULL);
INSERT INTO offline_epoch VALUES(1,lower(hex(randomblob(16))));
CREATE TABLE offline_devices(namespace TEXT PRIMARY KEY,session_hash TEXT NOT NULL,device_id TEXT NOT NULL,epoch TEXT NOT NULL,issued_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,revoked INTEGER NOT NULL DEFAULT 0,UNIQUE(session_hash,device_id));
CREATE TABLE offline_packs(id TEXT PRIMARY KEY,namespace TEXT NOT NULL REFERENCES offline_devices(namespace) ON DELETE CASCADE,manifest_json TEXT NOT NULL,sources_json TEXT NOT NULL);
CREATE TABLE offline_operation_receipts(namespace TEXT NOT NULL,uuid TEXT NOT NULL,payload_hash TEXT NOT NULL,receipt_json TEXT NOT NULL,PRIMARY KEY(namespace,uuid));
CREATE TABLE offline_organizing_drafts(id TEXT PRIMARY KEY,namespace TEXT NOT NULL,source_type TEXT NOT NULL,source_id TEXT NOT NULL,snapshot_id TEXT NOT NULL,content TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL DEFAULT(datetime('now')));
