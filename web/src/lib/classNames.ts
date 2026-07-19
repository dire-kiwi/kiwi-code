export type ClassNameValue = string | false | null | undefined

export function classNames(...values: ClassNameValue[]): string | undefined {
  const result = values.filter(Boolean).join(' ')
  return result || undefined
}
