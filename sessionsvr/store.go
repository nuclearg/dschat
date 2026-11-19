package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dschat/app/core/apierr"
	"dschat/app/core/model"
	"dschat/app/core/xdb"
)

const ProtocolVersion = 3

type Device struct {
	ID        string
	Name      string
	Platform  string
	CreatedAt int64
}

type SessionSummary struct {
	model.Session
	HasOpenTurn bool `json:"hasOpenTurn"`
}

type Store struct {
	db  *sql.DB
	dir string
}

const maxAttachmentSize = 64 << 20

func OpenStore(dir string) (*Store, error) {
	db, err := xdb.Open(filepath.Join(dir, "sessionsvr.db"))
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, dir: dir}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func migrate(db *sql.DB) error {
	schema := `
create table if not exists meta (key text primary key, value text not null);
create table if not exists devices (
  id text primary key,
  name text not null,
  platform text not null,
  token_hash text not null unique,
  created_at integer not null,
  last_seen_at integer not null default 0
);
create table if not exists sessions (
  id text primary key,
  title text not null default '',
  owner_device_id text not null,
  created_at integer not null,
  updated_at integer not null,
  deleted_at integer not null default 0
);
create table if not exists turns (
  id text primary key,
  session_id text not null references sessions(id) on delete cascade,
  parent_turn_id text not null,
  q_json text not null,
  created_at integer not null
);
create index if not exists turns_session on turns(session_id);
create index if not exists turns_parent on turns(parent_turn_id);
create table if not exists answers (
  turn_id text primary key references turns(id) on delete cascade,
  a_json text not null,
  created_at integer not null
);
create table if not exists auth_sessions (
  id text primary key,
  device_id text not null references devices(id) on delete cascade,
  access_hash text not null unique,
  access_expires_at integer not null,
  refresh_hash text not null unique,
  refresh_expires_at integer not null,
  revoked_at integer not null default 0,
  created_at integer not null,
  last_used_at integer not null default 0
);
create index if not exists auth_sessions_device on auth_sessions(device_id);
create table if not exists attachments (
  id text primary key,
  session_id text not null default '',
  name text not null,
  mime text not null,
  size integer not null,
  sha256 text not null default '',
  created_at integer not null
);
`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	_, _ = db.Exec(`alter table sessions add column deleted_at integer not null default 0`)
	_, _ = db.Exec(`insert or ignore into meta(key, value) values('server_id', ?)`, model.NewID())
	return nil
}

type Attachment struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId,omitempty"`
	Name      string `json:"name"`
	MIME      string `json:"mime"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

func (s *Store) attachmentDir() string { return filepath.Join(s.dir, "attachments") }

// SaveAttachment writes the blob atomically and upserts its metadata.
func (s *Store) SaveAttachment(a Attachment, r io.Reader) error {
	if a.ID == "" || strings.ContainsAny(a.ID, "/\\") {
		return apierr.New(400, "INVALID_ATTACHMENT", "invalid attachment id")
	}
	dir := s.attachmentDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, maxAttachmentSize+1))
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxAttachmentSize {
		return apierr.New(413, "FILE_TOO_LARGE", "attachment is too large")
	}
	path := filepath.Join(dir, a.ID)
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	a.Size = n
	a.SHA256 = hex.EncodeToString(h.Sum(nil))
	if a.CreatedAt == 0 {
		a.CreatedAt = model.NowMS()
	}
	if _, err := s.db.Exec(`insert or replace into attachments(id,session_id,name,mime,size,sha256,created_at)
values(?,?,?,?,?,?,?)`, a.ID, a.SessionID, a.Name, a.MIME, a.Size, a.SHA256, a.CreatedAt); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func (s *Store) CreateAuthSession(deviceID string, accessTTL, refreshTTL time.Duration) (string, string, int64, error) {
	access, err := newToken()
	if err != nil {
		return "", "", 0, err
	}
	refresh, err := newToken()
	if err != nil {
		return "", "", 0, err
	}
	now := time.Now()
	accessExp := now.Add(accessTTL)
	refreshExp := now.Add(refreshTTL)
	_, err = s.db.Exec(`insert into auth_sessions(id,device_id,access_hash,access_expires_at,refresh_hash,refresh_expires_at,revoked_at,created_at,last_used_at)
values(?,?,?,?,?,?,0,?,0)`,
		model.NewID(), deviceID, hashToken(access), accessExp.UnixMilli(), hashToken(refresh), refreshExp.UnixMilli(), now.UnixMilli())
	if err != nil {
		return "", "", 0, err
	}
	return access, refresh, int64(accessTTL / time.Second), nil
}

func (s *Store) DeviceByAccess(token string) (*Device, error) {
	var d Device
	err := s.db.QueryRow(`
select d.id,d.name,d.platform,d.created_at
from auth_sessions a join devices d on d.id=a.device_id
where a.access_hash=? and a.revoked_at=0 and a.access_expires_at>?`,
		hashToken(token), time.Now().UnixMilli()).
		Scan(&d.ID, &d.Name, &d.Platform, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apierr.New(401, "UNAUTHORIZED", "invalid or expired access token")
	}
	if err != nil {
		return nil, err
	}
	_, _ = s.db.Exec(`update auth_sessions set last_used_at=? where access_hash=?`, model.NowMS(), hashToken(token))
	return &d, nil
}

func (s *Store) RotateRefresh(token string, accessTTL, refreshTTL time.Duration) (string, string, int64, *Device, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", 0, nil, err
	}
	defer tx.Rollback()
	var id, deviceID string
	var refreshExp, revokedAt int64
	err = tx.QueryRow(`select id,device_id,refresh_expires_at,revoked_at from auth_sessions where refresh_hash=?`, hashToken(token)).
		Scan(&id, &deviceID, &refreshExp, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) || revokedAt != 0 || refreshExp < time.Now().UnixMilli() {
		return "", "", 0, nil, apierr.New(401, "UNAUTHORIZED", "invalid or expired refresh token")
	}
	if err != nil {
		return "", "", 0, nil, err
	}
	var d Device
	if err := tx.QueryRow(`select id,name,platform,created_at from devices where id=?`, deviceID).
		Scan(&d.ID, &d.Name, &d.Platform, &d.CreatedAt); err != nil {
		return "", "", 0, nil, err
	}
	if _, err := tx.Exec(`update auth_sessions set revoked_at=? where id=?`, model.NowMS(), id); err != nil {
		return "", "", 0, nil, err
	}
	access, err := newToken()
	if err != nil {
		return "", "", 0, nil, err
	}
	refresh, err := newToken()
	if err != nil {
		return "", "", 0, nil, err
	}
	now := time.Now()
	if _, err := tx.Exec(`insert into auth_sessions(id,device_id,access_hash,access_expires_at,refresh_hash,refresh_expires_at,revoked_at,created_at,last_used_at)
values(?,?,?,?,?,?,0,?,0)`,
		model.NewID(), deviceID, hashToken(access), now.Add(accessTTL).UnixMilli(), hashToken(refresh), now.Add(refreshTTL).UnixMilli(), now.UnixMilli()); err != nil {
		return "", "", 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", "", 0, nil, err
	}
	return access, refresh, int64(accessTTL / time.Second), &d, nil
}

func (s *Store) RevokeRefresh(token string) error {
	_, err := s.db.Exec(`update auth_sessions set revoked_at=? where refresh_hash=? and revoked_at=0`, model.NowMS(), hashToken(token))
	return err
}

func (s *Store) ClearAuthSessions() error {
	_, err := s.db.Exec(`delete from auth_sessions`)
	return err
}

func (s *Store) AttachmentMeta(id string) (*Attachment, error) {
	var a Attachment
	err := s.db.QueryRow(`select id,session_id,name,mime,size,sha256,created_at from attachments where id=?`, id).
		Scan(&a.ID, &a.SessionID, &a.Name, &a.MIME, &a.Size, &a.SHA256, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apierr.New(404, "ATTACHMENT_NOT_FOUND", "attachment not found")
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) AttachmentPath(id string) (string, *Attachment, error) {
	a, err := s.AttachmentMeta(id)
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(s.attachmentDir(), a.ID)
	if _, err := os.Stat(path); err != nil {
		return "", nil, apierr.New(404, "ATTACHMENT_NOT_FOUND", "attachment file missing")
	}
	return path, a, nil
}

func (s *Store) ServerID() (string, error) {
	var v string
	err := s.db.QueryRow(`select value from meta where key='server_id'`).Scan(&v)
	return v, err
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Store) CreateDevice(id, name, platform string) (*Device, string, error) {
	token, err := newToken()
	if err != nil {
		return nil, "", err
	}
	if id == "" {
		id = model.NewID()
	}
	d := &Device{ID: id, Name: name, Platform: platform, CreatedAt: model.NowMS()}
	if d.Name == "" {
		d.Name = d.ID[:8]
	}
	_, err = s.db.Exec(`insert into devices(id,name,platform,token_hash,created_at,last_seen_at) values(?,?,?,?,?,?)
on conflict(id) do update set name=excluded.name, platform=excluded.platform, token_hash=excluded.token_hash`,
		d.ID, d.Name, d.Platform, hashToken(token), d.CreatedAt, d.CreatedAt)
	if err != nil {
		return nil, "", err
	}
	return d, token, nil
}

func (s *Store) DeviceByToken(token string) (*Device, error) {
	var d Device
	err := s.db.QueryRow(`select id,name,platform,created_at from devices where token_hash=?`, hashToken(token)).
		Scan(&d.ID, &d.Name, &d.Platform, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apierr.New(401, "UNAUTHORIZED", "invalid token")
	}
	if err != nil {
		return nil, err
	}
	_, _ = s.db.Exec(`update devices set last_seen_at=? where id=?`, model.NowMS(), d.ID)
	return &d, nil
}

func (s *Store) CreateSession(id, title, owner string) (*model.Session, error) {
	if id == "" {
		id = model.NewID()
	}
	now := model.NowMS()
	_, err := s.db.Exec(`insert into sessions(id,title,owner_device_id,created_at,updated_at) values(?,?,?,?,?)`,
		id, title, owner, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, apierr.New(409, "SESSION_EXISTS", "session already exists")
		}
		return nil, err
	}
	return &model.Session{ID: id, Title: title, OwnerID: owner, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) ListSessions() ([]SessionSummary, error) {
	rows, err := s.db.Query(`
select s.id, s.title, s.owner_device_id, s.created_at, s.updated_at,
  exists(select 1 from turns t left join answers a on a.turn_id=t.id
         where t.session_id=s.id and a.turn_id is null) as open_turn
from sessions s where s.deleted_at=0 order by s.updated_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionSummary{}
	for rows.Next() {
		var x SessionSummary
		if err := rows.Scan(&x.ID, &x.Title, &x.OwnerID, &x.CreatedAt, &x.UpdatedAt, &x.HasOpenTurn); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) GetSession(id string) (*model.Session, []model.Turn, error) {
	var x model.Session
	err := s.db.QueryRow(`select id,title,owner_device_id,created_at,updated_at from sessions where id=? and deleted_at=0`, id).
		Scan(&x.ID, &x.Title, &x.OwnerID, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, apierr.New(404, "SESSION_NOT_FOUND", "session not found")
	}
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.db.Query(`
select t.id, t.parent_turn_id, t.q_json, t.created_at, a.a_json
from turns t left join answers a on a.turn_id=t.id
where t.session_id=? order by t.created_at asc, t.id asc`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	turns := []model.Turn{}
	for rows.Next() {
		var t model.Turn
		var qJSON string
		var aJSON sql.NullString
		if err := rows.Scan(&t.ID, &t.ParentTurnID, &qJSON, &t.CreatedAt, &aJSON); err != nil {
			return nil, nil, err
		}
		t.SessionID = id
		if err := json.Unmarshal([]byte(qJSON), &t.Q); err != nil {
			return nil, nil, fmt.Errorf("decode q for %s: %w", t.ID, err)
		}
		if aJSON.Valid {
			var a model.Answer
			if err := json.Unmarshal([]byte(aJSON.String), &a); err != nil {
				return nil, nil, fmt.Errorf("decode answer for %s: %w", t.ID, err)
			}
			t.Answer = &a
		}
		turns = append(turns, t)
	}
	return &x, turns, rows.Err()
}

func (s *Store) ownerOf(tx *sql.Tx, sessionID string) (string, error) {
	var owner string
	err := tx.QueryRow(`select owner_device_id from sessions where id=? and deleted_at=0`, sessionID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", apierr.New(404, "SESSION_NOT_FOUND", "session not found")
	}
	return owner, err
}

func (s *Store) AppendTurn(sessionID, caller string, t model.Turn) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	owner, err := s.ownerOf(tx, sessionID)
	if err != nil {
		return err
	}
	if owner != caller {
		return apierr.New(409, "NOT_OWNER", "caller is not the session owner")
	}
	if t.ID == "" {
		return apierr.New(400, "INVALID_TURN", "turn id is required")
	}
	if t.ID == model.RootTurnID {
		return apierr.New(400, "INVALID_TURN", "turn id 0 is reserved")
	}
	if t.ParentTurnID == "" {
		t.ParentTurnID = model.RootTurnID
	}
	if t.ParentTurnID != model.RootTurnID {
		var ok int
		err := tx.QueryRow(`
select count(1) from turns t join answers a on a.turn_id=t.id
where t.id=? and t.session_id=?`, t.ParentTurnID, sessionID).Scan(&ok)
		if err != nil {
			return err
		}
		if ok == 0 {
			return apierr.New(409, "PARENT_OPEN", "parent turn does not exist or has no answer")
		}
	}
	qJSON, err := json.Marshal(t.Q)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`insert into turns(id,session_id,parent_turn_id,q_json,created_at) values(?,?,?,?,?)`,
		t.ID, sessionID, t.ParentTurnID, string(qJSON), t.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			var existing string
			_ = tx.QueryRow(`select q_json from turns where id=?`, t.ID).Scan(&existing)
			if existing == string(qJSON) {
				return nil
			}
			return apierr.New(409, "TURN_EXISTS", "turn id already exists")
		}
		return err
	}
	if _, err := tx.Exec(`update sessions set updated_at=? where id=?`, model.NowMS(), sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AppendAnswer(sessionID, caller, turnID string, a model.Answer) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	owner, err := s.ownerOf(tx, sessionID)
	if err != nil {
		return err
	}
	if owner != caller {
		return apierr.New(409, "NOT_OWNER", "caller is not the session owner")
	}
	var exists int
	if err := tx.QueryRow(`select count(1) from turns where id=? and session_id=?`, turnID, sessionID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return apierr.New(404, "TURN_NOT_FOUND", "turn not found")
	}
	aJSON, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`insert into answers(turn_id,a_json,created_at) values(?,?,?)`, turnID, string(aJSON), a.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			var existing string
			_ = tx.QueryRow(`select a_json from answers where turn_id=?`, turnID).Scan(&existing)
			if existing == string(aJSON) {
				return nil
			}
			return apierr.New(409, "ANSWER_EXISTS", "turn already has an answer")
		}
		return err
	}
	if a.Title != "" {
		_, _ = tx.Exec(`update sessions set title=? where id=? and title=''`, a.Title, sessionID)
	}
	if _, err := tx.Exec(`update sessions set updated_at=? where id=?`, model.NowMS(), sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// Claim transfers ownership. When force is false, an open turn aborts the claim.
func (s *Store) Claim(sessionID, caller string, force bool) (*model.Session, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	owner, err := s.ownerOf(tx, sessionID)
	if err != nil {
		return nil, err
	}
	if owner == caller {
		return s.sessionIn(tx, sessionID)
	}
	if !force {
		var openTurn string
		err := tx.QueryRow(`
select t.id from turns t left join answers a on a.turn_id=t.id
where t.session_id=? and a.turn_id is null limit 1`, sessionID).Scan(&openTurn)
		if err == nil {
			return nil, apierr.New(409, "OPEN_TURN", "session has an unfinished turn").With("turnId", openTurn)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	if _, err := tx.Exec(`update sessions set owner_device_id=?, updated_at=? where id=?`, caller, model.NowMS(), sessionID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetSessionMeta(sessionID)
}

func (s *Store) GetSessionMeta(id string) (*model.Session, error) {
	var x model.Session
	err := s.db.QueryRow(`select id,title,owner_device_id,created_at,updated_at from sessions where id=? and deleted_at=0`, id).
		Scan(&x.ID, &x.Title, &x.OwnerID, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apierr.New(404, "SESSION_NOT_FOUND", "session not found")
	}
	return &x, err
}

func (s *Store) sessionIn(tx *sql.Tx, id string) (*model.Session, error) {
	var x model.Session
	err := tx.QueryRow(`select id,title,owner_device_id,created_at,updated_at from sessions where id=? and deleted_at=0`, id).
		Scan(&x.ID, &x.Title, &x.OwnerID, &x.CreatedAt, &x.UpdatedAt)
	return &x, err
}

func (s *Store) Rename(sessionID, caller, title string) error {
	res, err := s.db.Exec(`update sessions set title=?, updated_at=? where id=? and owner_device_id=? and deleted_at=0`,
		title, model.NowMS(), sessionID, caller)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Distinguish missing session from not-owner.
		owner, err := s.owner(sessionID)
		if err != nil {
			return err
		}
		if owner != caller {
			return apierr.New(409, "NOT_OWNER", "caller is not the session owner")
		}
	}
	return nil
}

func (s *Store) owner(sessionID string) (string, error) {
	var owner string
	err := s.db.QueryRow(`select owner_device_id from sessions where id=? and deleted_at=0`, sessionID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", apierr.New(404, "SESSION_NOT_FOUND", "session not found")
	}
	return owner, err
}

// DeleteSession soft-deletes a session. Blobs and rows are removed by
// CleanupExpired after the seven-day recovery window.
func (s *Store) DeleteSession(sessionID, caller string) error {
	var owner string
	var deletedAt int64
	err := s.db.QueryRow(`select owner_device_id, deleted_at from sessions where id=?`, sessionID).Scan(&owner, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return apierr.New(404, "SESSION_NOT_FOUND", "session not found")
	}
	if err != nil {
		return err
	}
	if deletedAt > 0 {
		// Idempotent retry after a lost response.
		return nil
	}
	if owner != caller {
		return apierr.New(409, "NOT_OWNER", "caller is not the session owner")
	}
	_, err = s.db.Exec(`update sessions set deleted_at=?, updated_at=? where id=? and deleted_at=0`,
		model.NowMS(), model.NowMS(), sessionID)
	return err
}

const DeletedSessionTTL = 7 * 24 * time.Hour

// CleanupExpired hard-deletes soft-deleted sessions whose recovery window has
// elapsed, including attachment blobs.
func (s *Store) CleanupExpired(cutoff int64) (int, error) {
	rows, err := s.db.Query(`select id from sessions where deleted_at>0 and deleted_at<=?`, cutoff)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	removed := 0
	for _, id := range ids {
		attRows, err := s.db.Query(`select id from attachments where session_id=?`, id)
		if err != nil {
			return removed, err
		}
		var attachmentIDs []string
		for attRows.Next() {
			var aid string
			if err := attRows.Scan(&aid); err == nil {
				attachmentIDs = append(attachmentIDs, aid)
			}
		}
		attRows.Close()
		tx, err := s.db.Begin()
		if err != nil {
			return removed, err
		}
		if _, err := tx.Exec(`delete from attachments where session_id=?`, id); err != nil {
			tx.Rollback()
			return removed, err
		}
		if _, err := tx.Exec(`delete from sessions where id=?`, id); err != nil {
			tx.Rollback()
			return removed, err
		}
		if err := tx.Commit(); err != nil {
			return removed, err
		}
		for _, aid := range attachmentIDs {
			_ = os.Remove(filepath.Join(s.attachmentDir(), aid))
		}
		removed++
	}
	return removed, nil
}

func (s *Store) TouchDevice(id string) {
	_, _ = s.db.Exec(`update devices set last_seen_at=? where id=?`, model.NowMS(), id)
}

var _ = os.Getenv
var _ = time.Now
