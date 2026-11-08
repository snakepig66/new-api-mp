# 词元市场 · 视觉改版规范（DeepSeek 风格）

替换 FRONTEND-REBUILD.md §3。原青绿色 #0E9F8E / #087266 作废，以本文为准。
只借鉴风格，不使用 DeepSeek 的名字、鲸鱼图标、字标或任何素材。

## 0. 先修（与设计无关，但最显眼）
模型详情页白屏：/models/:id 报 Failed to fetch dynamically imported module。
修复：deploy.sh 先构建、再重编译 Go 二进制、再重启；index.html 返回 Cache-Control: no-cache、/assets/* 返回 immutable；路由 errorComponent 遇 chunk 加载失败自动 location.reload() 一次，错误页中文。
占位文案全部下线：页脚「运营主体：待定」「ICP 备案号：待填」没有就整行不显示；首页实时数据条没数据就隐藏；/models 的「示例」徽标与示例说明删掉；/docs 的用户协议、隐私政策移出做单独页面；错误码表按 400/401/402/429/503 排序、左对齐。

## 1. 设计令牌（直接替换 styles.css 的 @theme）
@theme {
  --font-sans: "Inter", -apple-system, BlinkMacSystemFont, "PingFang SC", "HarmonyOS Sans SC", "Microsoft YaHei", "Noto Sans CJK SC", sans-serif;
  --font-mono: "JetBrains Mono Variable", ui-monospace, Menlo, Consolas, monospace; /* 只用于代码 */
  --color-bg: #ffffff;
  --color-bg-soft: #f7f8fa;
  --color-ink: #0f1115;
  --color-ink-2: #4b5160;
  --color-ink-3: #8a909c;
  --color-line: #eceef2;
  --color-line-strong: #dfe2e8;
  --color-brand: #4d6bfe;
  --color-brand-hover: #3b58f0;
  --color-brand-soft: #eef1ff;
  --color-brand-ink: #2f4bd8;
  --color-cta: #0f1115;
  --color-cta-hover: #2a2d34;
  --color-danger: #e5484d;
  --color-warn: #f59e0b;
  --radius-sm: 8px;
  --radius-md: 12px;
  --radius-lg: 20px;
  --radius-pill: 999px;
}
body { font-size: 15px; line-height: 1.75; color: var(--color-ink); background: #fff; }
规则：全站只有一个强调色（蓝）；成功绿/薄荷底/青线全删；「省 70%」蓝字+brand-soft 底；「空闲」灰色；「高峰」橙色小圆点不铺底；等宽字体只在代码块；表格数字 sans + tabular-nums；无阴影，卡片最多 1px line 边框。

## 2. 字号
首页 hero 56px（移动 36px）/600 行高 1.15 字距 -0.02em；页面标题 32px/600；区块标题 24px/600；正文 15px/400 行高 1.75；导航 14px/500；表格 14px 行高 52px；辅助说明 13px ink-3。不再出现 11–12.5px 正文。

## 3. 组件
主要按钮：近黑实心胶囊（--color-cta），高 40px（大 48px），左右 20px，白字 14–15px/500。
次要按钮：白底胶囊 1px line-strong 边框黑字。文字链接蓝色 hover 下划线。
输入框：高 48px 圆角 12px 底 bg-soft 无边框，focus 1px 蓝边 + 3px brand-soft 光晕；密码框带显示/隐藏。
导航：高 64px 白底底 1px 线；左字标，中间导航 14px 灰字激活黑字+2px 蓝下划线，右搜索胶囊（⌘K）、登录文字、注册黑色胶囊。
表格：无外框，表头 bg-soft 13px ink-3，行间 1px line，hover bg-soft，数字右对齐。
代码块：底 #0d1117 圆角 12px，顶部语言 tab（curl/Python/Node.js），右上复制按钮。
标签：圆角 999px 高 22px 12px 字，只用灰或蓝。
Logo：删折线图标，纯小写字标（如「词元」600 字重）配蓝色小圆点/方块。

## 4. 逐页改法
首页 /：顶部 640px 淡蓝白渐变 linear-gradient(180deg,#eef1ff,#fff)；hero 居中 56px「同一个 Key，用官方模型更便宜」+ 16px 灰副标 + 黑「开始使用」白「浏览模型」胶囊；下方 20px 圆角白卡放可复制 curl + base_url；实时数据条有真数据才显示（三列 32px 大数字无框）；三步上手蓝色圆圈序号；热门模型统一表格最多 6 行 + 「查看全部模型 →」蓝链；成为分享者 bg-soft 大圆角块右黑胶囊；区块间距 120px，max-width 1200px。
模型广场 /models：标题 32px + 一行说明（多少模型/最低倍率）；顶部胶囊切换器（全部/DeepSeek/...）+ 右侧搜索框，无左侧栏；表格列：模型（15px 黑名+13px 灰 slug）、输入价、输出价、最低倍率（蓝字）、到手价、分享者数、可用率、当前时段；行高 56px 整行可点。
模型详情 /models/:id：模型名 32px + 厂商标签 + 能力标签行；两栏：左价格卡（官方价/最低到手价/峰谷时段条）+ 挂单表，右 sticky 接入卡（base_url/模型名/复制/三语言 tab）+「加到我的 Key」黑胶囊。
文档 /docs：三栏——左导航 240px（激活 brand-soft 胶囊蓝字）、中正文 max 760px、右「本页目录」200px 滚动跟随（激活 2px 蓝左边线）；顶部搜索胶囊；代码块带语言 tab + 复制。
登录/注册：独立布局无全站导航页脚；左右分屏，左 45% 深蓝渐变 #1c2b8f→#4d6bfe 白标语三条卖点，右白底表单 max 380px（字标/标题/48px 输入/全宽黑胶囊/「或」分隔/GitHub 白胶囊/底部蓝字链接）。
控制台（使用者/分享者/运营）：左栏 220px 白底，分组标题 12px ink-3，激活 brand-soft 胶囊蓝字，底部身份切换（使用者⇄分享者）；内容区 bg-soft，卡片白底圆角 12px 无阴影 padding 24px；概览四数字卡（32px）+ 单色蓝图表 + 最近记录表；图表只用蓝灰。

## 5. 验收
全站 grep 不到 #0e9f8e、#087266、mint、teal、rounded-[4px]、text-[11、text-[12.5px]；除代码块不出现 font-mono/font-numeric；首页/广场/文档/登录四页与 DeepSeek 对应页并排截图同一路数；1440/390 无溢出无白屏无占位；/models/:id 部署后首访与硬刷新正常。
