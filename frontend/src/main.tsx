import { StrictMode } from "react"
import { createRoot } from "react-dom/client"

import "./index.css"
import App from "./App.tsx"
import { ThemeProvider } from "@/components/theme-provider.tsx"
import { Toaster } from "@/components/ui/sonner"

// A tab opened before a rebuild still asks for the previous build's
// hash-named chunks, which no longer exist. Reload once to pick up the new
// build instead of failing to open the page; the time guard stops a reload
// loop when a chunk is missing for some other reason.
const chunkReloadKey = "workavera:chunk-reload-at"
window.addEventListener("vite:preloadError", (event) => {
  const lastReload = Number(sessionStorage.getItem(chunkReloadKey) ?? 0)
  if (Date.now() - lastReload < 10_000) return
  sessionStorage.setItem(chunkReloadKey, String(Date.now()))
  event.preventDefault()
  window.location.reload()
})

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <App />
      <Toaster />
    </ThemeProvider>
  </StrictMode>
)
