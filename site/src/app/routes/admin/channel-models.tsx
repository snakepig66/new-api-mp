import { createFileRoute, redirect } from '@tanstack/react-router'

/**
 * 旧的渠道模型映射页已并入「模型池」的渠道模型标签页。
 * 保留这个路径做跳转，避免收藏或文档里的旧链接失效。
 */
export const Route = createFileRoute('/admin/channel-models')({
  beforeLoad: () => {
    throw redirect({ to: '/admin/pool', replace: true })
  },
})
