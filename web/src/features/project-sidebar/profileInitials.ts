/**
 * The letters shown on a profile's rail icon: the first character of each of
 * the first two words, so "Personal" is "P" and "Client work" is "CW".
 */
export function profileInitials(name: string) {
  const words = name.trim().split(/\s+/).filter(Boolean)
  if (words.length === 0) return '?'
  // Array.from splits by code point, so an emoji-led name keeps its whole emoji.
  return words.slice(0, 2).map((word) => Array.from(word)[0]).join('').toLocaleUpperCase()
}
