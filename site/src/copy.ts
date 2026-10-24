/**
 * 全站中文文案。所有界面文字从这里取，不要在组件里写死。
 * 产品名目前是占位名「词元市场」，等品牌定稿后只改 brand.name。
 */

export const copy = {
  brand: {
    name: '词元市场',
    tagline: '同一个 Key，用官方模型更便宜',
    description:
      '把分散的官方模型额度汇总成一个市场：价格、倍率、可用率都摆在同一张表里，按量结算，失败不收费。',
  },

  nav: {
    ariaMain: '主导航',
    ariaMobile: '移动端导航',
    home: '首页',
    models: '模型广场',
    docs: '文档',
    seller: '成为分享者',
    console: '控制台',
    admin: '运营后台',
    login: '登录',
    register: '注册',
    logout: '退出登录',
  },

  actions: {
    browseModels: '浏览模型',
    becomeSeller: '成为分享者',
    viewDocs: '查看接入文档',
    backToModels: '返回模型广场',
    backHome: '返回首页',
    createKey: '创建 Key',
    retry: '重试',
    close: '关闭',
    login: '登录',
    register: '注册',
    startOnboarding: '开始开通',
  },

  common: {
    loading: '加载中',
    emptyTitle: '暂时没有内容',
    nextStep: '下一步',
    sample: '示例',
    sampleNotice: '以下为版式示例，实时行情接入后由后端数据替换。',
    dataPending: '实时数据接入后显示',
    none: '—',
    unitSuffix: '/',
    comingSoon: '该功能将在下一阶段开放。',
  },

  tiers: {
    peak: '高峰',
    offpeak: '空闲',
    flat: '当前',
  },

  units: {
    perMillionTokens: '百万 tokens',
  },

  price: {
    otherTier: (label: string) => `${label}价`,
  },

  multiplier: {
    savings: (percent: string) => `省 ${percent}%`,
    baseline: '倍率',
  },

  peak: {
    peak: '高峰',
    offpeak: '空闲',
    nextSwitch: '距切换',
    imminent: '即将切换',
  },

  identity: {
    seller: '分享者',
    buyer: '买家',
  },

  footer: {
    copyright: '© 2026 词元市场',
    company: '运营主体：待定',
    icp: 'ICP 备案号：待填',
    terms: '用户协议',
    privacy: '隐私政策',
    note: '充值余额不可提现。',
  },

  pages: {
    home: {
      kicker: '官方模型额度，按量结算',
      headline: '同一个 Key，用官方模型更便宜',
      headlineLines: ['同一个 Key，', '用官方模型更便宜'],
      lead: '分享者把闲置的官方额度挂上市场，买家按当前倍率取用。价格写在明面上，调用失败不收费。',
      facts: [
        { label: '计费', value: '按实际 tokens' },
        { label: '失败调用', value: '不计费' },
        { label: '分享者结算', value: 'T+7' },
      ],
      stats: {
        models: '在售模型',
        lowestMultiplier: '最低倍率',
        callsToday: '今日调用量',
      },
      why: {
        title: '买得明白，用得踏实',
        items: [
          {
            title: '价格同屏比较',
            body: '官方价、最低倍率、到手价并排展示，峰谷档位和下次切换时间都写清楚。',
          },
          {
            title: '失败不收费',
            body: '只有成功的调用计费。重试链路和每一笔扣费都能在使用记录里查到。',
          },
          {
            title: '一个 Key 通用',
            body: '不用为每家供应商单独申请账号和 Key，换模型不换接入方式。',
          },
        ],
      },
      steps: {
        title: '三步跑通第一笔调用',
        hint: '从注册到发出第一个请求，通常不超过五分钟。',
        items: [
          { title: '注册账号', body: '一个账号同时管理买家与分享者身份。' },
          { title: '创建 Key', body: '选中要用的模型，设置每个模型的价格上限。' },
          { title: '粘贴请求', body: '把 base_url 指向本站，模型名用广场里的完整 ID。' },
        ],
      },
      seller: {
        title: '有闲置的官方额度？',
        body: '把它挂上市场，按你定的倍率接单。平台按 T+7 结算，提现记录可查。',
      },
    },

    models: {
      title: '模型广场',
      lead: '官方价与市场最低倍率并排比较，按当前时段结算。',
      priceUnit: '价格单位：¥ / 百万 tokens',
      sellerUnit: '位',
      outputPrefix: '出',
      filtersSoon: '厂商、能力和「只看有货」筛选将在实时行情接入后开放。',
      columns: {
        model: '模型',
        vendor: '厂商',
        official: '官方价（输入 / 输出）',
        multiplier: '最低倍率',
        buyerPrice: '到手价（输入 / 输出）',
        sellers: '分享者',
        availability: '可用率',
        latency: '延迟 P50',
        tier: '当前时段',
      },
      sample: [
        {
          id: 'deepseek-v3',
          name: 'DeepSeek V3',
          vendor: '深度求索',
          officialInput: 2_000_000,
          officialOutput: 8_000_000,
          multiplierMilli: 300,
          buyerInput: 600_000,
          buyerOutput: 2_400_000,
          sellers: 3,
          availability: '99.2%',
          latency: '820 ms',
          tier: 'offpeak' as const,
        },
        {
          id: 'qwen3-235b',
          name: 'Qwen3 235B',
          vendor: '阿里云',
          officialInput: 2_400_000,
          officialOutput: 9_600_000,
          multiplierMilli: 450,
          buyerInput: 1_080_000,
          buyerOutput: 4_320_000,
          sellers: 2,
          availability: '98.6%',
          latency: '1.1 s',
          tier: 'peak' as const,
        },
        {
          id: 'deepseek-r1',
          name: 'DeepSeek R1',
          vendor: '深度求索',
          officialInput: 4_000_000,
          officialOutput: 16_000_000,
          multiplierMilli: 520,
          buyerInput: 2_080_000,
          buyerOutput: 8_320_000,
          sellers: 1,
          availability: '97.4%',
          latency: '1.8 s',
          tier: 'offpeak' as const,
        },
        {
          id: 'glm-4-plus',
          name: 'GLM-4 Plus',
          vendor: '智谱',
          officialInput: 5_000_000,
          officialOutput: 20_000_000,
          multiplierMilli: 680,
          buyerInput: 3_400_000,
          buyerOutput: 13_600_000,
          sellers: 1,
          availability: '99.0%',
          latency: '740 ms',
          tier: 'peak' as const,
        },
      ],
      empty: {
        title: '暂时没有在售模型',
        reason: '市场还没有可用的分享者挂单，所以这里没有价格可报。',
        next: '你可以先看接入文档，或者成为第一个分享者。',
      },
    },

    modelDetail: {
      title: '模型详情',
      lead: '官方价目、挂单列表和可直接粘贴的代码示例。',
      empty: {
        title: '这个模型暂时没有报价',
        reason: (model: string) => `「${model}」当前没有可用的分享者挂单。`,
        next: '回到模型广场看看其他模型，或成为分享者挂上你的额度。',
      },
      sections: {
        official: '官方价目',
        listings: '挂单列表',
        code: '代码示例',
      },
    },

    docs: {
      title: '文档',
      lead: '一页讲清接入、计费与错误处理。每段代码都可以直接粘贴。',
      toc: '目录',
      sections: {
        quickstart: '快速开始',
        billing: '计费规则',
        errors: '错误码',
        terms: '用户协议',
        privacy: '隐私政策',
      },
      quickstart: {
        body: '把请求地址指向本站的 /v1，模型名使用模型广场里的完整 ID，Authorization 使用控制台创建的 Key。',
        note: 'BASE_URL 换成你正在访问的站点地址；Key 只显示一次，请立即保存。',
        code: `curl "$BASE_URL/v1/chat/completions" \\
  -H "Authorization: Bearer $API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "deepseek-v3", "messages": [{"role": "user", "content": "你好"}]}'`,
      },
      billing: {
        body: '高峰与空闲两档价格以模型详情页的当前档为准，页面同时给出下次切换时间。输入、输出、缓存读写分别计价，价格按百万 tokens 展示。',
        items: [
          '只有成功的请求计费，失败的请求不计费。',
          '搜索结果与工具调用按次计价，具体单价在模型详情页列出。',
          '充值余额不可提现，提现仅适用于分享者收入。',
        ],
      },
      errors: {
        body: '接口错误使用稳定的错误码，界面按错误码显示中文提示。',
        rows: [
          { code: '401', meaning: 'Key 无效或已失效', action: '检查 Authorization，必要时新建 Key。' },
          { code: '402', meaning: '余额不足', action: '充值后重试。' },
          { code: '400', meaning: '参数错误或模型未加入 Key', action: '核对模型 ID，并把模型加到当前 Key。' },
          { code: '429', meaning: '触发限流', action: '按响应中的提示退避重试。' },
          { code: '503', meaning: '暂无可用供应商', action: '稍后重试，或换一个时段。' },
        ],
        columns: { code: '错误码', meaning: '含义', action: '处理方式' },
      },
      terms: {
        body: '用户协议正式文本将在上线前发布。当前阶段接入即表示你同意按实际调用量结算，并遵守模型供应商的使用条款。',
      },
      privacy: {
        body: '隐私政策正式文本将在上线前发布。当前阶段我们只保存提供服务所必需的账号、调用与结算数据。',
      },
    },

    login: {
      title: '登录',
      lead: '登录后管理 Key、用量、账单与挂单。',
      brandHeading: '同一个 Key，用官方模型更便宜',
      brandBody:
        '买家用一个 Key 调用广场上的模型；分享者把官方额度挂上市场。价格、倍率和可用率都公开。',
      username: '用户名',
      usernamePlaceholder: '输入用户名',
      password: '密码',
      passwordPlaceholder: '输入密码',
      submit: '登录',
      submitting: '正在登录',
      failed: '登录失败，请检查用户名和密码。',
      noAccount: '还没有账号？',
      toRegister: '创建账号',
      note: '登录状态自动续期，15 分钟无操作后仍会保持。',
    },

    register: {
      title: '注册',
      lead: '创建账号后即可使用模型广场、控制台和分享者功能。',
      emptyTitle: '注册入口正在接入',
      emptyReason: '邮箱验证与风控流程还在接入中，当前阶段请使用已有账号登录。',
      emptyNext: '如果你已经有账号，直接登录即可。',
      action: '去登录',
    },

    console: {
      title: '控制台',
      lead: '余额、用量、节省与最近请求。',
      stats: {
        balance: '余额',
        spendThisMonth: '本月花费',
        savedThisMonth: '本月节省',
      },
      empty: {
        title: '还没有调用记录',
        reason: '创建 Key 并把模型加进 Key 之后，用量、费用和节省会出现在这里。',
        next: '先读接入文档，拿到可以直接粘贴的调用示例。',
      },
      keySoon: 'Key 管理将在控制台下一阶段开放。',
    },

    seller: {
      title: '分享者',
      lead: '把闲置的官方额度挂到市场，按你定的倍率接单。',
      steps: ['签署分享者协议', '完成实名认证', '绑定支付宝收款账户'],
      stats: {
        pending: '待结算（T+7）',
        withdrawable: '可提现',
        incomeThisMonth: '本月收入',
      },
      empty: {
        title: '还没有开通分享者',
        reason: '开通后才能挂单、查看收入与申请提现。',
        next: '开通流程包含协议签署、实名认证和收款账户绑定，全程约五分钟。',
      },
      onboardingSoon: '开通流程将在分享者阶段开放。',
    },

    admin: {
      title: '运营后台',
      lead: '价目表、挂单审核、提现审批与冲正。',
      stats: {
        pendingListings: '待审核挂单',
        pendingPayouts: '待审批提现',
        openReversals: '待处理冲正',
      },
      empty: {
        title: '运营后台正在搭建',
        reason: '市场运营工具尚未接入当前前端。',
        next: '上游渠道与系统设置暂时保留在旧后台入口，仅超管可访问。',
      },
      legacy: '旧后台入口：/ops-legacy。请求需携带管理员访问令牌。',
    },

    notFound: {
      title: '页面不存在',
      reason: '地址可能已经变化，或者这个页面还没有对外开放。',
      next: '回到首页，从模型广场重新开始。',
    },
  },

  validation: {
    required: '请填写这一项',
    password: '密码至少 6 位',
    mismatch: '两次输入不一致',
  },

  errors: {
    NETWORK_ERROR: '网络连接失败，请检查网络后重试。',
    TIMEOUT: '请求超时，请稍后重试。',
    AUTH_UNAUTHORIZED: '登录已失效，请重新登录。',
    AUTH_TOKEN_EXPIRED: '登录已过期，请重新登录。',
    AUTH_SESSION_REVOKED: '登录会话已失效，请重新登录。',
    AUTH_SESSION_REQUIRED: '这个操作需要重新验证登录状态。',
    AUTH_USER_DISABLED: '账号已被停用，请联系运营。',
    AUTH_INSUFFICIENT_PRIVILEGE: '当前账号没有这项权限。',
    AUTH_INTERNAL_ERROR: '服务暂时不可用，请稍后重试。',
    ACCESS_TOKEN_EXPIRED: '访问令牌已过期，请重新登录。',
    ACCESS_TOKEN_SCOPE_DENIED: '访问令牌缺少所需权限。',
    ACCESS_TOKEN_LEGACY_RETIRED: '旧访问令牌已停用，请重新创建。',
    invalid_api_key: 'Key 无效或已失效，请检查后重试。',
    insufficient_balance: '余额不足，请充值后重试。',
    invalid_request: '请求参数有误，请核对后重试。',
    no_supplier_available: '暂无可用供应商，请稍后重试。',
    UNKNOWN: '出了点问题，请稍后重试。',
  },
} as const

export type Copy = typeof copy
