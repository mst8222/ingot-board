-- 设备令牌范围（017-device-scopes）。
-- scopes 为 JSON：{"ops":["createTask",...],"project_ids":[1,3]}
-- NULL = 0008 之前的存量设备行，视为全量放行（已配对设备行为不变）；
-- 新签发设备一律写显式 JSON（配对码无范围则落 DefaultOps）。
-- 配对码同样带范围：签发时可收窄，兑换时随设备落库。

ALTER TABLE devices ADD COLUMN scopes TEXT;
ALTER TABLE pair_codes ADD COLUMN scopes TEXT;
