import { useEffect, useState, useCallback } from "react"
import { ChevronRight, ChevronDown, File, Folder, FolderOpen, Loader2 } from "lucide-react"
import { FileEntry } from "@/types"
import * as api from "@/services/api"
import { cn } from "@/lib/utils"

interface Props {
  agentId: string
}

interface TreeNode extends FileEntry {
  children?: TreeNode[]
  loaded?: boolean
  open?: boolean
}

export default function FileTree({ agentId }: Props) {
  const [nodes, setNodes] = useState<TreeNode[]>([])
  const [loading, setLoading] = useState(false)

  const loadDir = useCallback(
    async (path?: string): Promise<TreeNode[]> => {
      const res = await api.listFiles(agentId, path)
      return res.files
        .sort((a, b) => {
          if (a.is_dir && !b.is_dir) return -1
          if (!a.is_dir && b.is_dir) return 1
          return a.name.localeCompare(b.name)
        })
        .map((f) => ({ ...f, children: f.is_dir ? [] : undefined, loaded: false, open: false }))
    },
    [agentId]
  )

  useEffect(() => {
    setLoading(true)
    loadDir().then((items) => {
      setNodes(items)
      setLoading(false)
    }).catch(() => setLoading(false))
  }, [agentId, loadDir])

  const toggleDir = async (path: string) => {
    const toggle = async (items: TreeNode[]): Promise<TreeNode[]> => {
      return Promise.all(
        items.map(async (node) => {
          if (node.path === path && node.is_dir) {
            if (!node.loaded) {
              const children = await loadDir(node.path)
              return { ...node, children, loaded: true, open: true }
            }
            return { ...node, open: !node.open }
          }
          if (node.children) {
            return { ...node, children: await toggle(node.children) }
          }
          return node
        })
      )
    }
    setNodes(await toggle(nodes))
  }

  const handleFileClick = (node: TreeNode) => {
    if (node.is_dir) {
      toggleDir(node.path)
    } else {
      window.open(`/api/agents/${agentId}/files/${node.path}`, "_blank")
    }
  }

  const renderNode = (node: TreeNode, depth: number = 0) => (
    <div key={node.path}>
      <button
        className="flex items-center gap-1.5 w-full text-left px-2 py-1 text-xs hover:bg-accent/50 rounded transition-colors cursor-pointer"
        style={{ paddingLeft: `${depth * 16 + 8}px` }}
        onClick={() => handleFileClick(node)}
      >
        {node.is_dir ? (
          <>
            {node.open ? (
              <ChevronDown className="h-3 w-3 flex-shrink-0 text-muted-foreground" />
            ) : (
              <ChevronRight className="h-3 w-3 flex-shrink-0 text-muted-foreground" />
            )}
            {node.open ? (
              <FolderOpen className="h-3.5 w-3.5 flex-shrink-0 text-primary" />
            ) : (
              <Folder className="h-3.5 w-3.5 flex-shrink-0 text-primary" />
            )}
          </>
        ) : (
          <>
            <span className="w-3" />
            <File className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
          </>
        )}
        <span className="truncate">{node.name}</span>
      </button>
      {node.is_dir && node.open && node.children?.map((child) => renderNode(child, depth + 1))}
    </div>
  )

  if (loading) {
    return (
      <div className="flex justify-center py-8">
        <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
      </div>
    )
  }

  if (nodes.length === 0) {
    return <p className="text-center text-xs text-muted-foreground py-8">暂无文件</p>
  }

  return <div className="py-2">{nodes.map((n) => renderNode(n))}</div>
}
