# Vendor marks

The marketplace vendor pills read `logo_url` from `/api/marketplace/vendors`.
Each file is a 24×24 SVG with a brand-color rounded tile and a white mark.

- `deepseek.svg`, `qwen.svg`, `kimi.svg`, `minimax.svg`: mark path from
  [Simple Icons](https://simpleicons.org/) (CC0), recolored onto the tile.
- `zhipu.svg`, `doubao.svg`: project-owned simplified marks used because
  Simple Icons has no matching slug. Replace the file with an official SVG if
  one becomes available; the URL and backend mapping stay unchanged.

Vendor mapping (backend `marketplace/api_vendors.go`):

| vendor slug | file |
| --- | --- |
| `deepseek` | `deepseek.svg` |
| `zhipu` | `zhipu.svg` |
| `aliyun-bailian`, `aliyun-bailian-coding-plan` | `qwen.svg` |
| `moonshot` | `kimi.svg` |
| `minimax` | `minimax.svg` |
| `volcengine-ark`, `volcengine-ark-coding-plan` | `doubao.svg` |
