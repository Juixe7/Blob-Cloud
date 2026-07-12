/** @type {import('tailwindcss').Config} */
// Linear/Vercel dark-theme palette. The app is dark-only, so darkMode 'class'
// is paired with a hardcoded <html class="dark"> in index.html.
export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // Semantic aliases mapped to the spec's near-black zinc palette.
        ink: {
          950: '#09090b', // page background
          900: '#18181b', // cards / dialogs
          800: '#27272a', // structural borders, hover
        },
      },
      fontFamily: {
        sans: [
          'Inter',
          'Geist Sans',
          'ui-sans-serif',
          'system-ui',
          '-apple-system',
          'Segoe UI',
          'Roboto',
          'sans-serif',
        ],
        mono: ['Geist Mono', 'ui-monospace', 'SFMono-Regular', 'Consolas', 'monospace'],
      },
      boxShadow: {
        glow: '0 0 0 1px rgba(139,92,246,0.4), 0 0 24px -4px rgba(139,92,246,0.35)',
      },
      keyframes: {
        'fade-in': {
          '0%': { opacity: '0', transform: 'translateY(4px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
      },
      animation: {
        'fade-in': 'fade-in 0.2s ease-in-out',
      },
    },
  },
  plugins: [],
}
