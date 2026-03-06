import { Agent } from "@/types"
import FileTree from "./FileTree"
import DesktopEntry from "./DesktopEntry"
import { Separator } from "@/components/ui/separator"

interface Props {
  agent: Agent
}

export default function WorkspacePanel({ agent }: Props) {
  return (
    <div className="flex flex-col h-full">
      <div className="px-4 py-3 font-medium text-sm border-b border-border flex-shrink-0">
        Workspace
      </div>

      <div className="flex-1 overflow-y-auto">
        <FileTree agentId={agent.id} />
      </div>

      <Separator />
      <DesktopEntry agent={agent} />
    </div>
  )
}
