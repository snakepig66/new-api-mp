import { createFileRoute, redirect } from '@tanstack/react-router'

/**
 * 旧的渠道模型映射页已并入「模型池」的渠道模型标签页。
 * 跳转时带上 tab 参数，保证深链接仍能直接打开该标签页。
 */
export const Route = createFileRoute('/admin/channel-models')({
  beforeLoad: () => {
    throw redirect({
      to: '/console/ops/models',
      search: { tab: 'channel-models' },
      replace: true,
    })
  },
})
