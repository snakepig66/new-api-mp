import type { ComponentProps, ReactNode } from 'react'

import { Label } from '@/legacy/components/ui/label'
import { Switch } from '@/legacy/components/ui/switch'
import { cn } from '@/legacy/lib/utils'

type SettingsSwitchRowProps = ComponentProps<'div'>

type SettingsSwitchFieldProps = SettingsSwitchRowProps & {
  controlId?: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  label: ReactNode
  description?: ReactNode
  disabled?: boolean
}

export function SettingsSwitchField({
  controlId,
  checked,
  onCheckedChange,
  label,
  description,
  disabled,
  className,
  ...props
}: SettingsSwitchFieldProps) {
  return (
    <div
      data-settings-form-span='full'
      className={cn(
        'flex min-w-0 flex-row items-center justify-between gap-4 py-2.5',
        className
      )}
      {...props}
    >
      <div className='min-w-0 space-y-0.5'>
        <Label htmlFor={controlId} className='text-sm font-medium'>
          {label}
        </Label>
        {description ? (
          <p
            id={controlId ? `${controlId}-description` : undefined}
            className='text-muted-foreground text-xs'
          >
            {description}
          </p>
        ) : null}
      </div>
      <Switch
        id={controlId}
        aria-describedby={
          controlId && description ? `${controlId}-description` : undefined
        }
        checked={checked}
        onCheckedChange={onCheckedChange}
        disabled={disabled}
      />
    </div>
  )
}
