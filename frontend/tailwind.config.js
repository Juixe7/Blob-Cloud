/** @type {import('tailwindcss').Config} */
// Linear/Vercel dark-theme palette. The app is dark-only, so darkMode 'class'
// is paired with a hardcoded <html class="dark"> in index.html.
export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // High-contrast, bespoke slate palette
        arch: {
          950: '#090a0b', // main canvas (zinc-950)
          900: '#18181b', // surface / panel (zinc-900)
          850: '#27272a', // elevated card (zinc-800)
          800: '#3f3f46', // structural card hover (zinc-700)
          700: '#52525b', // border / outline (zinc-600)
          border: '#27272a', // sharp dividers (zinc-800)
        },
        // Intentional sharp accent color (Electric Amber)
        amber: {
          500: '#f59e0b',
          400: '#fbbf24',
          600: '#d97706',
        },
      },
      fontFamily: {
        sans: ['"Plus Jakarta Sans"', '-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'Roboto', 'sans-serif'],
        display: ['"Syne"', '"Plus Jakarta Sans"', 'sans-serif'],
        mono: ['"JetBrains Mono"', 'ui-monospace', 'SFMono-Regular', 'Consolas', 'monospace'],
      },
      boxShadow: {
        sharp: '0 1px 3px 0 rgba(0, 0, 0, 0.4), 0 1px 2px -1px rgba(0, 0, 0, 0.4)',
        amber: '0 0 15px -3px rgba(245, 158, 11, 0.3)',
      },
      keyframes: {
        'fade-in': {
          '0%': { opacity: '0', transform: 'translateY(4px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        // Modal entrance: subtle scale + fade.
        'modal-in': {
          '0%': { opacity: '0', transform: 'scale(0.97) translateY(6px)' },
          '100%': { opacity: '1', transform: 'scale(1) translateY(0)' },
        },
        // Context-menu pop: scale from the click point.
        'menu-in': {
          '0%': { opacity: '0', transform: 'scale(0.95)' },
          '100%': { opacity: '1', transform: 'scale(1)' },
        },
      },
      animation: {
        'fade-in': 'fade-in 0.2s ease-in-out',
      },
    },
  },
  plugins: [
    require('@tailwindcss/forms')({
      strategy: 'class',
    }),
  ],
}
