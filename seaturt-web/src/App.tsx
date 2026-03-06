import { useEffect } from "react"
import Layout from "@/components/layout/Layout"

export default function App() {
  useEffect(() => {
    document.documentElement.classList.add("dark")
  }, [])

  return <Layout />
}
