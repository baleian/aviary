import type { Config } from "tailwindcss";

const config: Config = {
  darkMode: ["class", '[data-theme="dark"]'],
  content: [
    "./src/**/*.{ts,tsx,js,jsx}",
    "./src/app/**/*.{ts,tsx}",
    "./src/components/**/*.{ts,tsx}",
    "./src/features/**/*.{ts,tsx}",
  ],
  theme: {
    container: {
      center: true,
      padding: "1rem",
      screens: { "2xl": "1400px" },
    },
    extend: {
      fontFamily: {
        sans: ["var(--font-inter)", "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ["var(--font-mono)", "ui-monospace", "SFMono-Regular", "monospace"],
      },
      fontSize: {
        hero: ["24px", { lineHeight: "1.2", letterSpacing: "-0.015em", fontWeight: "600" }],
        h1: ["20px", { lineHeight: "1.25", letterSpacing: "-0.012em", fontWeight: "600" }],
        h2: ["16px", { lineHeight: "1.3", letterSpacing: "-0.008em", fontWeight: "600" }],
        h3: ["14px", { lineHeight: "1.35", letterSpacing: "-0.005em", fontWeight: "600" }],
        body: ["13.5px", { lineHeight: "1.55" }],
        small: ["12.5px", { lineHeight: "1.45" }],
        xs: ["11.5px", { lineHeight: "1.4" }],
        over: ["10.5px", { lineHeight: "1.3", letterSpacing: "0.08em", fontWeight: "600" }],
      },
      colors: {
        // Raw surface tokens
        canvas: "var(--bg-canvas)",
        surface: "var(--bg-surface)",
        raised: "var(--bg-raised)",
        sunk: "var(--bg-sunk)",
        hover: "var(--bg-hover)",
        "bg-active": "var(--bg-active)",
        overlay: "var(--bg-overlay)",

        // shadcn semantic slots
        background: "var(--background)",
        foreground: "var(--foreground)",
        card: {
          DEFAULT: "var(--card)",
          foreground: "var(--card-foreground)",
        },
        popover: {
          DEFAULT: "var(--popover)",
          foreground: "var(--popover-foreground)",
        },
        primary: {
          DEFAULT: "var(--primary)",
          foreground: "var(--primary-foreground)",
        },
        secondary: {
          DEFAULT: "var(--secondary)",
          foreground: "var(--secondary-foreground)",
        },
        muted: {
          DEFAULT: "var(--muted)",
          foreground: "var(--muted-foreground)",
        },
        accent: {
          DEFAULT: "var(--accent-blue)",
          strong: "var(--accent-blue-strong)",
          soft: "var(--accent-blue-soft)",
          border: "var(--accent-blue-border)",
          // shadcn accent slot
          bg: "var(--accent)",
          foreground: "var(--accent-foreground)",
        },
        destructive: {
          DEFAULT: "var(--destructive)",
          foreground: "var(--destructive-foreground)",
        },
        border: {
          DEFAULT: "var(--border)",
          subtle: "var(--border-subtle)",
          strong: "var(--border-strong)",
        },
        input: "var(--input)",
        ring: "var(--ring)",

        fg: {
          primary: "var(--fg-primary)",
          secondary: "var(--fg-secondary)",
          tertiary: "var(--fg-tertiary)",
          muted: "var(--fg-muted)",
          inverse: "var(--fg-inverse)",
        },

        status: {
          live: "var(--status-live)",
          "live-soft": "var(--status-live-soft)",
          warn: "var(--status-warn)",
          "warn-soft": "var(--status-warn-soft)",
          error: "var(--status-error)",
          "error-soft": "var(--status-error-soft)",
          info: "var(--status-info)",
          "info-soft": "var(--status-info-soft)",
        },
      },
      borderRadius: {
        xs: "4px",
        sm: "5px",
        DEFAULT: "7px",
        md: "7px",
        lg: "10px",
        xl: "12px",
      },
      boxShadow: {
        sm: "var(--shadow-sm)",
        DEFAULT: "var(--shadow-md)",
        md: "var(--shadow-md)",
        lg: "var(--shadow-lg)",
        xl: "var(--shadow-xl)",
      },
      transitionTimingFunction: {
        panel: "cubic-bezier(0.16, 1, 0.3, 1)",
      },
      transitionDuration: {
        fast: "120ms",
        panel: "180ms",
      },
      keyframes: {
        "fade-in": { from: { opacity: "0" }, to: { opacity: "1" } },
        "slide-up": {
          from: { opacity: "0", transform: "translateY(6px)" },
          to: { opacity: "1", transform: "translateY(0)" },
        },
        pulse: {
          "0%, 100%": { opacity: "1" },
          "50%": { opacity: "0.6" },
        },
      },
      animation: {
        "fade-in": "fade-in 180ms cubic-bezier(0.16, 1, 0.3, 1)",
        "slide-up": "slide-up 220ms cubic-bezier(0.16, 1, 0.3, 1)",
        pulse: "pulse 1.6s ease-in-out infinite",
      },
    },
  },
  plugins: [
    require("tailwindcss-animate"),
    require("@tailwindcss/typography"),
  ],
};

export default config;
