package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/mst8222/ingot-board/internal/domain"
)

// PhoneSettings 手机监听开关。Port 为 0 时由监听器在第一次打开时选定。
type PhoneSettings struct {
	LANEnabled    bool
	RemoteEnabled bool
	Port          int
}

// PushSubscription 一条 Web Push 订阅。
type PushSubscription struct {
	ID       int64
	DeviceID int64
	Endpoint string
	P256dh   string
	Auth     string
}

// GetPhoneSettings 读取唯一一行监听开关。
func (s *Store) GetPhoneSettings() (PhoneSettings, error) {
	var lan, remote, port int
	err := s.db.QueryRow(
		`SELECT lan_enabled, remote_enabled, port FROM phone_settings WHERE id=1`,
	).Scan(&lan, &remote, &port)
	if err != nil {
		return PhoneSettings{}, err
	}
	return PhoneSettings{LANEnabled: lan != 0, RemoteEnabled: remote != 0, Port: port}, nil
}

// SavePhoneSettings 写回监听开关。
func (s *Store) SavePhoneSettings(st PhoneSettings) error {
	lan, remote := 0, 0
	if st.LANEnabled {
		lan = 1
	}
	if st.RemoteEnabled {
		remote = 1
	}
	_, err := s.db.Exec(
		`UPDATE phone_settings SET lan_enabled=?, remote_enabled=?, port=? WHERE id=1`,
		lan, remote, st.Port)
	return err
}

// SavePairCode 保存一次性配对码的哈希、到期时间与范围（017）。
// scopesJSON 为空串表示未收窄（兑换时落 DefaultOps）。
func (s *Store) SavePairCode(codeHash string, expires time.Time, scopesJSON string) error {
	_, err := s.db.Exec(
		`INSERT INTO pair_codes(code_hash, expires_at, scopes) VALUES(?,?,?)`,
		codeHash, formatTime(expires), nullIfEmpty(scopesJSON))
	return err
}

// RedeemPairCode 把未使用且未过期的配对码标成已用，并取回其范围。
// 影响行数不是 1 时返回未授权，调用方无法区分过期、已用或不存在。
func (s *Store) RedeemPairCode(codeHash string, now time.Time) (string, error) {
	nows := formatTime(now)
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	// 先读后改都在事务里：一次性语义靠 UPDATE 影响行数保证。
	var scopes sql.NullString
	if err := tx.QueryRow(
		`SELECT scopes FROM pair_codes WHERE code_hash=? AND used_at IS NULL AND expires_at > ?`,
		codeHash, nows).Scan(&scopes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", domain.ErrUnauthorized
		}
		return "", err
	}
	res, err := tx.Exec(
		`UPDATE pair_codes SET used_at=? WHERE code_hash=? AND used_at IS NULL`,
		nows, codeHash)
	if err != nil {
		return "", err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return "", domain.ErrUnauthorized
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return scopes.String, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// CreateDevice 登记一台手机。tokenHash 是 atd_ 令牌的哈希；
// scopes 是 017 的显式范围（JSON 串，空串存 NULL = 全量，仅存量行如此）。
func (s *Store) CreateDevice(name, tokenHash, scopesJSON string) (*domain.Device, error) {
	if name == "" {
		name = "手机"
	}
	now := formatTime(nowUTC())
	res, err := s.db.Exec(
		`INSERT INTO devices(name, token_hash, created_at, scopes) VALUES(?,?,?,?)`,
		name, tokenHash, now, nullIfEmpty(scopesJSON))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetDevice(id)
}

// GetDevice 按 ID 取未吊销的设备。
func (s *Store) GetDevice(id int64) (*domain.Device, error) {
	d, revoked, err := s.scanDevice(s.db.QueryRow(
		`SELECT id, name, created_at, last_seen_at, revoked_at, scopes FROM devices WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if revoked {
		return nil, domain.ErrNotFound
	}
	return d, nil
}

// ListDevices 返回未吊销的设备，按 ID 排序。
func (s *Store) ListDevices() ([]*domain.Device, error) {
	rows, err := s.db.Query(
		`SELECT id, name, created_at, last_seen_at, revoked_at, scopes FROM devices WHERE revoked_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*domain.Device, 0)
	for rows.Next() {
		d, _, err := s.scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceByTokenHash 按令牌哈希取设备。已吊销视为不存在。
func (s *Store) DeviceByTokenHash(tokenHash string) (*domain.Device, error) {
	d, revoked, err := s.scanDevice(s.db.QueryRow(
		`SELECT id, name, created_at, last_seen_at, revoked_at, scopes FROM devices WHERE token_hash=?`, tokenHash))
	if err != nil {
		return nil, err
	}
	if revoked {
		return nil, domain.ErrUnauthorized
	}
	return d, nil
}

// TouchDevice 更新最后出现时间。
func (s *Store) TouchDevice(id int64) error {
	_, err := s.db.Exec(`UPDATE devices SET last_seen_at=? WHERE id=? AND revoked_at IS NULL`,
		formatTime(nowUTC()), id)
	return err
}

// RevokeDevice 吊销设备。已吊销或不存在都返回未找到。
func (s *Store) RevokeDevice(id int64) error {
	res, err := s.db.Exec(
		`UPDATE devices SET revoked_at=? WHERE id=? AND revoked_at IS NULL`,
		formatTime(nowUTC()), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrNotFound
	}
	return nil
}

type deviceScanner interface {
	Scan(dest ...any) error
}

func (s *Store) scanDevice(row deviceScanner) (*domain.Device, bool, error) {
	var d domain.Device
	var created, last, revoked, scopes sql.NullString
	if err := row.Scan(&d.ID, &d.Name, &created, &last, &revoked, &scopes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, domain.ErrNotFound
		}
		return nil, false, err
	}
	if sc, err := domain.ParseScopes(scopes); err != nil {
		return nil, false, err
	} else {
		d.Scopes = sc
	}
	if created.Valid {
		t, err := parseTime(created.String)
		if err != nil {
			return nil, false, err
		}
		d.CreatedAt = t
	}
	if last.Valid && last.String != "" {
		t, err := parseTime(last.String)
		if err != nil {
			return nil, false, err
		}
		d.LastSeenAt = &t
	}
	return &d, revoked.Valid && revoked.String != "", nil
}

// UpsertPushSubscription 按 endpoint 覆盖订阅，换手机登录时订阅跟着设备走。
func (s *Store) UpsertPushSubscription(deviceID int64, endpoint, p256dh, authKey string) error {
	_, err := s.db.Exec(`
		INSERT INTO push_subscriptions(device_id, endpoint, p256dh, auth, created_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(endpoint) DO UPDATE SET
			device_id=excluded.device_id,
			p256dh=excluded.p256dh,
			auth=excluded.auth`,
		deviceID, endpoint, p256dh, authKey, formatTime(nowUTC()))
	return err
}

// DeletePushSubscription 删除这台设备的一条订阅。
func (s *Store) DeletePushSubscription(deviceID int64, endpoint string) error {
	_, err := s.db.Exec(
		`DELETE FROM push_subscriptions WHERE device_id=? AND endpoint=?`, deviceID, endpoint)
	return err
}

// ListPushSubscriptions 返回全部订阅，供待评审通知发送。
func (s *Store) ListPushSubscriptions() ([]PushSubscription, error) {
	rows, err := s.db.Query(
		`SELECT id, device_id, endpoint, p256dh, auth FROM push_subscriptions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PushSubscription, 0)
	for rows.Next() {
		var sub PushSubscription
		if err := rows.Scan(&sub.ID, &sub.DeviceID, &sub.Endpoint, &sub.P256dh, &sub.Auth); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// DeletePushSubscriptionByEndpoint 推送端点失效时删掉它。
func (s *Store) DeletePushSubscriptionByEndpoint(endpoint string) error {
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint=?`, endpoint)
	return err
}

// RecentWorkdirs 按最近更新返回用过的工作目录。
func (s *Store) RecentWorkdirs(limit int) ([]string, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT workdir FROM tasks
		WHERE workdir <> ''
		GROUP BY workdir
		ORDER BY MAX(updated_at) DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var dir string
		if err := rows.Scan(&dir); err != nil {
			return nil, err
		}
		out = append(out, dir)
	}
	return out, rows.Err()
}
