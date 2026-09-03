import type { Config } from "tailwindcss";

const config: Config = {
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}", "./lib/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        felt: { DEFAULT: "#1f4d3d", dark: "#173a2e", light: "#2b6650" },
        card: { back: "#b23a3a", backAlt: "#2f5fa8", face: "#fdfbf5" },
        ink: "#1b1b1f",
      },
      fontFamily: {
        display: ["var(--font-display)", "ui-rounded", "system-ui", "sans-serif"],
      },
      keyframes: {
        pop: { "0%": { transform: "scale(0.85)", opacity: "0" }, "100%": { transform: "scale(1)", opacity: "1" } },
        slideUp: { "0%": { transform: "translateY(8px)", opacity: "0" }, "100%": { transform: "translateY(0)", opacity: "1" } },
      },
      animation: { pop: "pop 160ms ease-out", slideUp: "slideUp 180ms ease-out" },
    },
  },
  plugins: [],
};
export default config;
