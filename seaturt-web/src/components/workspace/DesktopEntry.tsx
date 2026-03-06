import { useEffect, useState } from "react"
import { Monitor, ExternalLink, Loader2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Agent, DesktopInfo } from "@/types"
import * as api from "@/services/api"

interface Props {
  agent: Agent
}

export default function DesktopEntry({ agent }: Props) {
  const [desktop, setDesktop] = useState<DesktopInfo | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (agent.status !== "running") {
      setDesktop(null)
      return
    }
    setLoading(true)
    api.getDesktop(agent.id).then((info) => {
      setDesktop(info)
      setLoading(false)
    }).catch(() => setLoading(false))
  }, [agent.id, agent.status])

  if (agent.status !== "running") {
    return (
      <div className="px-4 py-3">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Monitor className="h-4 w-4" />
          <span>桌面（Agent 未运行）</span>
        </div>
      </div>
    )
  }

  if (loading) {
    return (
      <div className="px-4 py-3 flex items-center gap-2">
        <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
        <span className="text-xs text-muted-foreground">加载桌面信息...</span>
      </div>
    )
  }

  if (!desktop || !desktop.kasmvnc_url) {
    return (
      <div className="px-4 py-3">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Monitor className="h-4 w-4" />
          <span>桌面不可用</span>
        </div>
      </div>
    )
  }

  return (
    <div className="px-4 py-3">
      <div className="flex items-center gap-2 mb-2 text-sm">
        <Monitor className="h-4 w-4" />
        <span className="font-medium">桌面</span>
      </div>
      <Button
        variant="outline"
        size="sm"
        className="w-full justify-start gap-2"
        onClick={() => window.open(desktop.kasmvnc_url, "_blank")}
      >
        <ExternalLink className="h-3.5 w-3.5" />
        打开 KasmVNC 桌面
      </Button>
    </div>
  )
}
