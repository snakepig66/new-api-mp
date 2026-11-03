import i18next, { type Resource } from 'i18next'
import { initReactI18next } from 'react-i18next'

import zh from './locales/zh.json'

const source = zh as unknown as { translation?: Record<string, string> }
const translation = source.translation ?? (zh as unknown as Record<string, string>)

const resources: Resource = {
  zhCN: { translation },
}

export const legacyI18n = i18next.createInstance()

void legacyI18n.use(initReactI18next).init({
  resources,
  lng: 'zhCN',
  fallbackLng: 'zhCN',
  supportedLngs: ['zhCN'],
  nsSeparator: false,
  interpolation: { escapeValue: false },
  react: { useSuspense: false },
})

export default legacyI18n
