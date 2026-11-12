package app

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/mst8222/ingot-board/internal/domain"
	"github.com/mst8222/ingot-board/internal/store"
)

// PairCodeTTL 配对码有效期。过期后不能再兑换设备令牌。
const PairCodeTTL = 5 * time.Minute

// PhoneSettings 是监听开关在应用层的形状。
type PhoneSettings = store.PhoneSettings

// PhoneRuntime 手机监听当前对外公布的地址。
type PhoneRuntime struct {
	Settings           PhoneSettings
	LANURL             string
	RemoteURL          string
	RemoteError        string
	TailscaleInstalled bool
	Ready              bool
}

// PhoneApply 按开关打开或关闭手机监听，返回实际地址。
type PhoneApply func(PhoneSettings) (PhoneRuntime, error)

// SetPhoneApply 由 serve 注入。测试不注入时，开关只落库。
func (s *Service) SetPhoneApply(fn PhoneApply) {
	s.mu.Lock()
	s.applyPhone = fn
	s.mu.Unlock()
}

// SetReviewNotifier 任务进入待评审时调用。空订阅由实现自己跳过。
func (s *Service) SetReviewNotifier(fn func(taskID int64, title string)) {
	s.mu.Lock()
	s.onReview = fn
	s.mu.Unlock()
}

// SetVAPIDPublicKey 把公钥交给手机订阅接口。私钥留在数据目录。
func (s *Service) SetVAPIDPublicKey(key string) {
	s.mu.Lock()
	s.vapidPublic = key
	s.mu.Unlock()
}

// VAPIDPublicKey 返回 Web Push 公钥。尚未生成时为空。
func (s *Service) VAPIDPublicKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vapidPublic
}

// SetPhoneRuntime 记录监听器报上来的地址。
func (s *Service) SetPhoneRuntime(rt PhoneRuntime) {
	s.runtimeMu.Lock()
	rt.Ready = true
	s.runtime = rt
	s.runtimeMu.Unlock()
}

// PhoneStatus 返回最近一次监听结果；还没开过则只有库里的开关。
func (s *Service) PhoneStatus() (PhoneRuntime, error) {
	s.runtimeMu.Lock()
	rt := s.runtime
	s.runtimeMu.Unlock()
	if rt.Ready {
		return rt, nil
	}
	st, err := s.store.GetPhoneSettings()
	if err != nil {
		return PhoneRuntime{}, err
	}
	rt.Settings = st
	return rt, nil
}

// UpdatePhoneSettings 先让监听器生效，成功后再落库。
func (s *Service) UpdatePhoneSettings(in PhoneSettings) (PhoneRuntime, error) {
	cur, err := s.store.GetPhoneSettings()
	if err != nil {
		return PhoneRuntime{}, err
	}
	in.Port = cur.Port
	s.mu.Lock()
	apply := s.applyPhone
	s.mu.Unlock()
	if apply != nil {
		rt, err := apply(in)
		if err != nil {
			return PhoneRuntime{}, errors.Join(domain.ErrInvalidInput, err)
		}
		in.Port = rt.Settings.Port
		in.LANEnabled = rt.Settings.LANEnabled
		in.RemoteEnabled = rt.Settings.RemoteEnabled
		if err := s.store.SavePhoneSettings(in); err != nil {
			return PhoneRuntime{}, err
		}
		s.SetPhoneRuntime(rt)
		return rt, nil
	}
	if err := s.store.SavePhoneSettings(in); err != nil {
		return PhoneRuntime{}, err
	}
	rt := PhoneRuntime{Settings: in, Ready: true}
	s.SetPhoneRuntime(rt)
	return rt, nil
}

// IssuePairCode 签发一次性配对码。明文只在返回值里出现。
// scopes 非 nil 时可收窄范围（017）：操作清单须全部已注册；nil 表示缺省范围。
func (s *Service) IssuePairCode(now time.Time, scopes *domain.DeviceScopes) (string, time.Time, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	code := "atp_" + hex.EncodeToString(b)
	expires := now.Add(PairCodeTTL)
	var scopesJSON string
	if scopes != nil {
		if err := scopes.Validate(); err != nil {
			return "", time.Time{}, err
		}
		raw, err := scopes.Marshal()
		if err != nil {
			return "", time.Time{}, err
		}
		scopesJSON = raw
	}
	if err := s.store.SavePairCode(HashToken(code), expires, scopesJSON); err != nil {
		return "", time.Time{}, err
	}
	return code, expires, nil
}

// PairDevice 用配对码换一台设备和 atd_ 令牌。
// 设备范围继承配对码：码上没写就落缺省范围（013 清单），
// 不存在没有范围字段的第二种令牌。
func (s *Service) PairDevice(code, name string) (*domain.Device, string, error) {
	code = strings.TrimSpace(code)
	if !strings.HasPrefix(code, "atp_") {
		return nil, "", errors.Join(domain.ErrUnauthorized, errors.New("配对码无效或已使用"))
	}
	scopesJSON, err := s.store.RedeemPairCode(HashToken(code), time.Now().UTC())
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorized) {
			return nil, "", errors.Join(domain.ErrUnauthorized, errors.New("配对码无效或已使用"))
		}
		return nil, "", err
	}
	var scopes *domain.DeviceScopes
	if scopesJSON != "" {
		if scopes, err = domain.ParseScopes(sql.NullString{String: scopesJSON, Valid: true}); err != nil {
			return nil, "", err
		}
	} else {
		scopes = &domain.DeviceScopes{Ops: domain.DefaultOps(), ProjectIDs: make([]int64, 0)}
	}
	plain, hash, err := generateDeviceToken()
	if err != nil {
		return nil, "", err
	}
	raw, err := scopes.Marshal()
	if err != nil {
		return nil, "", err
	}
	dev, err := s.store.CreateDevice(strings.TrimSpace(name), hash, raw)
	if err != nil {
		return nil, "", err
	}
	return dev, plain, nil
}

// AuthenticateDevice 校验 atd_ 令牌。吊销和伪造都是未授权。
func (s *Service) AuthenticateDevice(token string) (*domain.Device, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "atd_") {
		return nil, domain.ErrUnauthorized
	}
	dev, err := s.store.DeviceByTokenHash(HashToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrUnauthorized
		}
		return nil, err
	}
	_ = s.store.TouchDevice(dev.ID)
	return dev, nil
}

// ListDevices 列出未吊销的手机。
func (s *Service) ListDevices() ([]*domain.Device, error) {
	return s.store.ListDevices()
}

// RevokeDevice 吊销一台手机。
func (s *Service) RevokeDevice(id int64) error {
	return s.store.RevokeDevice(id)
}

// SavePushSubscription 保存这台设备的推送订阅。
func (s *Service) SavePushSubscription(deviceID int64, endpoint, p256dh, authKey string) error {
	if endpoint == "" || p256dh == "" || authKey == "" {
		return errors.Join(domain.ErrInvalidInput, errors.New("推送订阅不完整"))
	}
	return s.store.UpsertPushSubscription(deviceID, endpoint, p256dh, authKey)
}

// DeletePushSubscription 删除这台设备的一条推送订阅。
func (s *Service) DeletePushSubscription(deviceID int64, endpoint string) error {
	if endpoint == "" {
		return errors.Join(domain.ErrInvalidInput, errors.New("缺少推送端点"))
	}
	return s.store.DeletePushSubscription(deviceID, endpoint)
}

// RecentWorkdirs 给手机新建任务用的最近目录。
func (s *Service) RecentWorkdirs(limit int) ([]string, error) {
	return s.store.RecentWorkdirs(limit)
}

func generateDeviceToken() (plain, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	plain = "atd_" + hex.EncodeToString(b)
	hash = HashToken(plain)
	return plain, hash, nil
}
