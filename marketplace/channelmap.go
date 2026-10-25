package marketplace

// channelmap.go — 渠道模型映射表 (channel_model_map).
//
// 云平台托管的模型并不总是与上游渠道的模型同名：例如火山引擎方舟把
// DeepSeek-V3.2 暴露为 deepseek-v3-2-flash，阿里云百炼暴露为
// deepseek-v3.2-flash。本表维护“平台标准模型 ID → 某 vendor 的上游模型名”
// 的对应关系，后台可编辑；只有本表里存在的模型才允许上架（§官方渠道白名单）。
//
// 唯一性约束是刻意的：同一 vendor 下 model 与 upstream_model 各自唯一。
// 如果两个平台模型映射到同一个上游模型，计价会选中哪一行、探测/巡检该用哪个
// 上游名字都会产生歧义，因此这里必须由数据库兜底，而不是靠调用方自觉。

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
)

// ChannelModelMap maps one vendor's upstream model name to the platform's
// standard model id. Unique per (vendor, model) and (vendor, upstream_model).
type ChannelModelMap struct {
	ID            int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	Vendor        string `gorm:"size:64;not null;uniqueIndex:idx_mp_channel_model_vendor_model;uniqueIndex:idx_mp_channel_model_vendor_upstream" json:"vendor"`
	Model         string `gorm:"size:128;not null;uniqueIndex:idx_mp_channel_model_vendor_model" json:"model"`
	UpstreamModel string `gorm:"size:128;not null;uniqueIndex:idx_mp_channel_model_vendor_upstream" json:"upstream_model"`
}

func (ChannelModelMap) TableName() string { return "mp_channel_model_map" }

// Channel-model mapping semantic errors. They deliberately carry fixed English
// text: the admin API forwards them verbatim, and they must never echo anything
// beyond the caller's own input (there is no key material in this module).
var (
	// ErrChannelModelEmpty is returned when vendor, model or upstream_model is
	// blank after trimming.
	ErrChannelModelEmpty = errors.New("vendor, model and upstream_model are required")
	// ErrChannelModelUpstreamTaken is returned when the requested upstream model
	// is already mapped to a different platform model under the same vendor.
	ErrChannelModelUpstreamTaken = errors.New("upstream_model is already mapped to another model for this vendor")
)

// MigrateChannelModelMap creates or upgrades mp_channel_model_map. It is
// idempotent and safe to call on every startup.
func MigrateChannelModelMap(db *gorm.DB) error {
	if err := db.AutoMigrate(&ChannelModelMap{}); err != nil {
		return fmt.Errorf("marketplace: migrate channel model map: %w", err)
	}
	return nil
}

// channelModelSeed is one row of the initial mapping seed.
type channelModelSeed struct {
	Vendor        string
	Model         string
	UpstreamModel string
}

// channelModelSeeds is the official whitelist seed: the two platform models
// DeepSeek serves under their own names, the names the other official
// gateways expose for the same models, and one self-mapped example per
// remaining vendor.
//
// moonshot / zhipu / minimax intentionally have no price-book rows yet. They
// appear in this table (so their upstream names are documented) but callers
// build the listable-model list by intersecting this table with the price
// book, so an unpriced platform model never becomes listable. That filtering
// is the caller's responsibility.
var channelModelSeeds = []channelModelSeed{
	// DeepSeek official API uses the platform model ids verbatim.
	{Vendor: "deepseek", Model: "deepseek-flash", UpstreamModel: "deepseek-flash"},
	{Vendor: "deepseek", Model: "deepseek-v4-pro", UpstreamModel: "deepseek-v4-pro"},
	// 火山引擎方舟 (Volcengine Ark) uses its own versioned model ids.
	{Vendor: "volcengine-ark", Model: "deepseek-flash", UpstreamModel: "deepseek-v3-2-flash"},
	{Vendor: "volcengine-ark", Model: "deepseek-v4-pro", UpstreamModel: "deepseek-v3-2"},
	// 阿里云百炼 (Aliyun Bailian) uses dotted version ids.
	{Vendor: "aliyun-bailian", Model: "deepseek-flash", UpstreamModel: "deepseek-v3.2-flash"},
	{Vendor: "aliyun-bailian", Model: "deepseek-v4-pro", UpstreamModel: "deepseek-v3.2"},
	// Self-mapped examples. No price book yet → not listable (see above).
	{Vendor: "moonshot", Model: "kimi-k2", UpstreamModel: "kimi-k2"},
	{Vendor: "zhipu", Model: "glm-4.6", UpstreamModel: "glm-4.6"},
	{Vendor: "minimax", Model: "minimax-m2", UpstreamModel: "minimax-m2"},
}

// SeedChannelModelMap idempotently inserts the official channel-model mapping
// seed. A row is skipped when (vendor, model) already exists — including one an
// administrator has edited — so Init can call this on every startup without
// clobbering operator changes.
func SeedChannelModelMap(db *gorm.DB) error {
	for _, s := range channelModelSeeds {
		var cnt int64
		if err := db.Model(&ChannelModelMap{}).
			Where("vendor = ? AND model = ?", s.Vendor, s.Model).
			Count(&cnt).Error; err != nil {
			return fmt.Errorf("marketplace: seed channel model map: %w", err)
		}
		if cnt > 0 {
			continue
		}
		row := ChannelModelMap{
			Vendor:        s.Vendor,
			Model:         s.Model,
			UpstreamModel: s.UpstreamModel,
		}
		if err := db.Create(&row).Error; err != nil {
			return fmt.Errorf("marketplace: seed channel model map: %w", err)
		}
	}
	// Vendors injected through MP_EXTRA_VENDORS (H8, e.g. the tipig E2E
	// gateway) are env-configured test sources: their model ids are already
	// the upstream names, so seed an identity mapping. This keeps the listing
	// validation uniform without hard-coding an unofficial vendor here.
	for _, vendorID := range VendorIDs() {
		if builtinVendorByID(vendorID) {
			continue
		}
		vendor, ok := VendorByID(vendorID)
		if !ok {
			continue
		}
		for _, model := range vendor.Models {
			var cnt int64
			if err := db.Model(&ChannelModelMap{}).
				Where("vendor = ? AND model = ?", vendor.ID, model).
				Count(&cnt).Error; err != nil {
				return fmt.Errorf("marketplace: seed channel model map: %w", err)
			}
			if cnt > 0 {
				continue
			}
			row := ChannelModelMap{Vendor: vendor.ID, Model: model, UpstreamModel: model}
			if err := db.Create(&row).Error; err != nil {
				return fmt.Errorf("marketplace: seed channel model map: %w", err)
			}
		}
	}
	return nil
}

// ChannelModelResolve returns the upstream model name for a platform model of
// a vendor, and whether a mapping exists.
func ChannelModelResolve(db *gorm.DB, vendor, model string) (upstream string, ok bool) {
	if db == nil {
		return "", false
	}
	var row ChannelModelMap
	err := db.Select("upstream_model").
		Where("vendor = ? AND model = ?", strings.TrimSpace(vendor), strings.TrimSpace(model)).
		Take(&row).Error
	if err != nil {
		return "", false
	}
	return row.UpstreamModel, true
}

// ChannelUpstreamModel is the same lookup as ChannelModelResolve, kept for
// callers that prefer the upstream-oriented name.
func ChannelUpstreamModel(db *gorm.DB, vendor, model string) (string, bool) {
	return ChannelModelResolve(db, vendor, model)
}

// ChannelModelsFor returns the platform standard model ids mapped for vendor,
// deduplicated and sorted so callers get a stable list across calls.
//
// The signature has no error by contract; a database failure yields an empty
// list (and therefore no listable models) rather than guessing.
func ChannelModelsFor(db *gorm.DB, vendor string) []string {
	if db == nil {
		return []string{}
	}
	var models []string
	if err := db.Model(&ChannelModelMap{}).
		Where("vendor = ?", strings.TrimSpace(vendor)).
		Pluck("model", &models).Error; err != nil {
		return []string{}
	}
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))
	for _, m := range models {
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// UpsertChannelModel creates or updates the mapping of (vendor, model).
//
// Validation: all three fields are trimmed and must be non-empty. Uniqueness:
// (vendor, model) identifies the row, while (vendor, upstream_model) must not
// collide with another model — otherwise two platform models would resolve to
// the same upstream model and pricing/probing would be ambiguous. A collision
// returns ErrChannelModelUpstreamTaken.
func UpsertChannelModel(db *gorm.DB, vendor, model, upstream string) error {
	vendor = strings.TrimSpace(vendor)
	model = strings.TrimSpace(model)
	upstream = strings.TrimSpace(upstream)
	if vendor == "" || model == "" || upstream == "" {
		return ErrChannelModelEmpty
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var existing ChannelModelMap
		err := tx.Where("vendor = ? AND model = ?", vendor, model).Take(&existing).Error
		found := true
		if errors.Is(err, gorm.ErrRecordNotFound) {
			found = false
		} else if err != nil {
			return fmt.Errorf("marketplace: query channel model map: %w", err)
		}

		// Check the other unique key before writing so the caller gets a
		// semantic error instead of a dialect-specific constraint violation.
		var clash int64
		if err := tx.Model(&ChannelModelMap{}).
			Where("vendor = ? AND upstream_model = ? AND model <> ?", vendor, upstream, model).
			Count(&clash).Error; err != nil {
			return fmt.Errorf("marketplace: query channel model map: %w", err)
		}
		if clash > 0 {
			return ErrChannelModelUpstreamTaken
		}

		if !found {
			row := ChannelModelMap{Vendor: vendor, Model: model, UpstreamModel: upstream}
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("marketplace: create channel model map: %w", err)
			}
			return nil
		}
		if existing.UpstreamModel == upstream {
			return nil
		}
		if err := tx.Model(&ChannelModelMap{}).
			Where("id = ?", existing.ID).
			Update("upstream_model", upstream).Error; err != nil {
			return fmt.Errorf("marketplace: update channel model map: %w", err)
		}
		return nil
	})
}

// DeleteChannelModel removes the (vendor, model) mapping. Deleting an absent
// row is a no-op so an idempotent admin DELETE always succeeds.
func DeleteChannelModel(db *gorm.DB, vendor, model string) error {
	vendor = strings.TrimSpace(vendor)
	model = strings.TrimSpace(model)
	if vendor == "" || model == "" {
		return ErrChannelModelEmpty
	}
	if err := db.Where("vendor = ? AND model = ?", vendor, model).
		Delete(&ChannelModelMap{}).Error; err != nil {
		return fmt.Errorf("marketplace: delete channel model map: %w", err)
	}
	return nil
}
