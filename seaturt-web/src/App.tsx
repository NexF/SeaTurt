import { useEffect } from "react"
import Layout from "@/components/layout/Layout"
import { useGlobalEvents } from "@/hooks/useGlobalEvents"

export default function App() {
  // Global SSE subscription for agent-level events (session_created, etc.)
  useGlobalEvents()

  useEffect(() => {
    const stored = localStorage.getItem("theme")
    if (stored === "light") {
      document.documentElement.classList.remove("dark")
    } else if (stored === "dark") {
      document.documentElement.classList.add("dark")
    } else {
      // Follow system preference
      if (window.matchMedia("(prefers-color-scheme: dark)").matches) {
        document.documentElement.classList.add("dark")
      } else {
        document.documentElement.classList.remove("dark")
      }
    }
  }, [])

  return <Layout />
}
