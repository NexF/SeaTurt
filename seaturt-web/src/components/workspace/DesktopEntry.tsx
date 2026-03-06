import { useEffect, useState, useRef, useCallback } from "react"
import { Monitor, ExternalLink, Loader2 } from "lucide-react"
import { Agent, DesktopInfo } from "@/types"
import * as api from "@/services/api"

interface Props {
  agent: Agent
}

type DesktopRole = "idle" | "waiting" | "shared"

// Module-level map to persist full-desktop window references across agent switches.
// When the component unmounts (agent switch) and remounts, we can recover the WindowProxy
// and restore the correct role instead of always resetting to "idle" (controller).
const fullDesktopWindows = new Map<string, Window>()
// Track agents whose desktop has been ready at least once (Selkies already started).
// When switching back to such an agent, skip the 2s startup delay.
const readyAgents = new Set<string>()

export default function DesktopEntry({ agent }: Props) {
  const [desktop, setDesktop] = useState<DesktopInfo | null>(null)
  const [loading, setLoading] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const [containerWidth, setContainerWidth] = useState(280)

  // Determine initial role: if a full-desktop window is still open for this agent,
  // start in "shared" mode; otherwise "idle" (controller).
  const [role, setRole] = useState<DesktopRole>(() => {
    const existingWin = fullDesktopWindows.get(agent.id)
    if (existingWin && !existingWin.closed) {
      return "shared"
    }
    fullDesktopWindows.delete(agent.id)
    return "idle"
  })
  const fullDesktopWindowRef = useRef<Window | null>(
    fullDesktopWindows.get(agent.id) ?? null
  )
  const switchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)


  // Delay iframe rendering: Selkies needs time to start after the container reports "running".
  // Show a loading spinner during the delay instead of loading an unready iframe.
  const [ready, setReady] = useState(() => {
    // If the desktop was already ready before (agent switch back), skip delay.
    if (readyAgents.has(agent.id)) return true
    // If recovering into shared mode, the desktop was already up — no need to delay.
    const existingWin = fullDesktopWindows.get(agent.id)
    return !!(existingWin && !existingWin.closed)
  })
  const readyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (agent.status !== "running") {
      setDesktop(null)
      setReady(false)
      readyAgents.delete(agent.id)
      return
    }
    setLoading(true)
    const alreadyReady = readyAgents.has(agent.id)
    api.getDesktop(agent.id).then((info) => {
      setDesktop(info)
      setLoading(false)
      if (alreadyReady) {
        // Desktop was already up before switch — no delay needed
        setReady(true)
      } else if (!ready) {
        readyTimerRef.current = setTimeout(() => {
          readyTimerRef.current = null
          setReady(true)
          readyAgents.add(agent.id)
        }, 2000)
      }
    }).catch(() => setLoading(false))
    return () => {
      if (readyTimerRef.current) {
        clearTimeout(readyTimerRef.current)
        readyTimerRef.current = null
      }
    }
  }, [agent.id, agent.status])

  // Poll to detect when the full desktop tab is closed (in waiting or shared)
  useEffect(() => {
    if (role === "idle") return
    const timer = setInterval(() => {
      if (!fullDesktopWindowRef.current || fullDesktopWindowRef.current.closed) {
        fullDesktopWindowRef.current = null
        fullDesktopWindows.delete(agent.id)
        if (switchTimerRef.current) {
          clearTimeout(switchTimerRef.current)
          switchTimerRef.current = null
        }
        setRole("idle")
      }
    }, 1000)
    return () => clearInterval(timer)
  }, [role])

  // Observe container width for responsive scaling
  useEffect(() => {
    if (!containerRef.current) return
    const obs = new ResizeObserver((entries) => {
      for (const e of entries) setContainerWidth(e.contentRect.width)
    })
    obs.observe(containerRef.current)
    return () => obs.disconnect()
  }, [])

  const handleClick = useCallback(() => {
    if (!desktop?.desktop_url) return
    if (role !== "idle") return
    // Open as a separate browser window (not a tab in the same window).
    // When controller and shared viewer are tabs in the same Chrome window,
    // Chrome throttles WebRTC in the inactive tab, causing the preview to black out.
    // A separate window keeps both connections active.
    const win = window.open(
      desktop.desktop_url,
      `seaturt-desktop-${agent.id}`,
      "noopener=no,width=1920,height=1080"
    )
    if (!win) return
    fullDesktopWindowRef.current = win
    fullDesktopWindows.set(agent.id, win)
    setRole("waiting")

    switchTimerRef.current = setTimeout(() => {
      switchTimerRef.current = null
      if (fullDesktopWindowRef.current && !fullDesktopWindowRef.current.closed) {
        setRole("shared")
      } else {
        fullDesktopWindowRef.current = null
        fullDesktopWindows.delete(agent.id)
        setRole("idle")
      }
    }, 3000)
  }, [desktop?.desktop_url, role])

  // Scale factor: map container width to a 1920px iframe
  const iframeW = 1920
  const iframeH = 1080
  const scale = containerWidth / iframeW
  const displayH = iframeH * scale

  if (agent.status !== "running") {
    return (
      <div className="px-4 py-3">
        <div className="flex items-center gap-2 text-xs text-muted-foreground mb-2">
          <Monitor className="h-4 w-4" />
          <span>桌面</span>
        </div>
        <div
          className="rounded-md border border-border bg-muted/30 flex items-center justify-center"
          style={{ height: `${Math.round(displayH)}px` }}
        >
          <span className="text-xs text-muted-foreground">Agent 未运行</span>
        </div>
      </div>
    )
  }

  if (loading) {
    return (
      <div className="px-4 py-3">
        <div className="flex items-center gap-2 text-xs text-muted-foreground mb-2">
          <Monitor className="h-4 w-4" />
          <span>桌面</span>
        </div>
        <div
          className="rounded-md border border-border bg-muted/30 flex items-center justify-center"
          style={{ height: `${Math.round(displayH)}px` }}
        >
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
        </div>
      </div>
    )
  }

  if (!desktop || !desktop.desktop_url) {
    return (
      <div className="px-4 py-3">
        <div className="flex items-center gap-2 text-xs text-muted-foreground mb-2">
          <Monitor className="h-4 w-4" />
          <span>桌面</span>
        </div>
        <div
          className="rounded-md border border-border bg-muted/30 flex items-center justify-center"
          style={{ height: `${Math.round(displayH)}px` }}
        >
          <span className="text-xs text-muted-foreground">桌面不可用</span>
        </div>
      </div>
    )
  }

  if (!ready) {
    return (
      <div className="px-4 py-3">
        <div className="flex items-center gap-2 text-xs text-muted-foreground mb-2">
          <Monitor className="h-4 w-4" />
          <span>桌面</span>
        </div>
        <div
          className="rounded-md border border-border bg-muted/30 flex items-center justify-center"
          style={{ height: `${Math.round(displayH)}px` }}
        >
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          <span className="ml-2 text-xs text-muted-foreground">桌面启动中…</span>
        </div>
      </div>
    )
  }

  return (
    <div className="px-4 py-3" ref={containerRef}>
      <div className="flex items-center justify-between mb-2">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Monitor className="h-4 w-4" />
          <span>桌面</span>
        </div>
        <button
          onClick={handleClick}
          className="flex items-center gap-1 text-xs text-primary hover:underline"
        >
          <ExternalLink className="h-3 w-3" />
          打开完整桌面
        </button>
      </div>
      <div
        className="relative rounded-md border border-border overflow-hidden cursor-pointer group"
        style={{ height: `${Math.round(displayH)}px` }}
        onClick={handleClick}
      >
        <iframe
          key={`${agent.id}-${role === "shared" ? "shared" : "ctrl"}`}
          src={role === "shared" ? `${desktop.desktop_url}#shared` : desktop.desktop_url}
          style={{
            width: `${iframeW}px`,
            height: `${iframeH}px`,
            transform: `scale(${scale})`,
            transformOrigin: "top left",
            border: "none",
          }}
          tabIndex={-1}
          title="Desktop Preview"
        />
        {/* Transparent overlay to capture clicks and block iframe interaction */}
        <div className="absolute inset-0 bg-transparent group-hover:bg-black/10 transition-colors flex items-center justify-center">
          <span className="opacity-0 group-hover:opacity-100 transition-opacity text-white text-xs bg-black/60 px-3 py-1.5 rounded-full">
            点击打开完整桌面
          </span>
        </div>
      </div>
    </div>
  )
}
