import { useState } from "react"
import { Agent } from "@/types"
import FileTree from "./FileTree"
import FileViewer from "./FileViewer"
import DesktopEntry from "./DesktopEntry"
import { Separator } from "@/components/ui/separator"

interface Props {
  agent: Agent
}

export default function WorkspacePanel({ agent }: Props) {
  const [previewFile, setPreviewFile] = useState<{ path: string; name: string } | null>(null)

  return (
    <div className="flex flex-col h-full">
      <div className="px-4 py-3 font-medium text-sm border-b border-border flex-shrink-0">
        Workspace
      </div>

      {previewFile ? (
        <div className="flex-1 overflow-hidden">
          <FileViewer
            agentId={agent.id}
            filepath={previewFile.path}
            filename={previewFile.name}
            onClose={() => setPreviewFile(null)}
          />
        </div>
      ) : (
        <>
          <div className="flex-1 overflow-y-auto">
            <FileTree
              agentId={agent.id}
              onFileClick={(path, name) => setPreviewFile({ path, name })}
            />
          </div>

          <Separator />
          <DesktopEntry agent={agent} />
        </>
      )}
    </div>
  )
}
