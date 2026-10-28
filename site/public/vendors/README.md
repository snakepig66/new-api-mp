# Vendor marks

The marketplace vendor pills read `logo_url` from `/api/marketplace/vendors`.
Each file is a 24×24 SVG: the vendor's official color mark, unaltered, centered
on a white rounded glass tile with padding.

Marks come from [@lobehub/icons-static-svg](https://github.com/lobehub/lobe-icons)
(MIT): `deepseek-color`, `doubao-color`, `kimi-color`, `minimax-color`,
`qwen-color`, `zhipu-color`. Brand marks remain trademarks of their owners.

Vendor mapping (backend `marketplace/api_vendors.go`):

| vendor slug | file |
| --- | --- |
| `deepseek` | `deepseek.svg` |
| `zhipu` | `zhipu.svg` |
| `aliyun-bailian`, `aliyun-bailian-coding-plan` | `qwen.svg` |
| `moonshot` | `kimi.svg` |
| `minimax` | `minimax.svg` |
| `volcengine-ark`, `volcengine-ark-coding-plan` | `doubao.svg` |
