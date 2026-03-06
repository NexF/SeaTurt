import { useEffect, useState } from "react"
import { X, ExternalLink, Loader2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"

interface Props {
  agentId: string
  filepath: string
  filename: string
  onClose: () => void
}

const imageExts = new Set([".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".ico"])
const textExts = new Set([
  ".md", ".txt", ".json", ".yaml", ".yml", ".toml", ".xml",
  ".js", ".ts", ".jsx", ".tsx", ".py", ".go", ".rs", ".rb",
  ".java", ".c", ".cpp", ".h", ".hpp", ".css", ".html", ".htm",
  ".sh", ".bash", ".zsh", ".fish", ".bat", ".ps1",
  ".sql", ".graphql", ".proto", ".csv", ".tsv",
  ".env", ".gitignore", ".dockerignore", ".editorconfig",
  ".ini", ".cfg", ".conf", ".log",
])

function getExt(name: string): string {
  const dot = name.lastIndexOf(".")
  return dot >= 0 ? name.slice(dot).toLowerCase() : ""
}

export default function FileViewer({ agentId, filepath, filename, onClose }: Props) {
  const [content, setContent] = useState<string>("")
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const ext = getExt(filename)
  const isImage = imageExts.has(ext)
  const isMarkdown = ext === ".md"
  const isText = textExts.has(ext)
  const fileUrl = `/api/agents/${agentId}/files/${filepath}`

  useEffect(() => {
    if (isImage) {
      setLoading(false)
      return
    }

    if (isText || isMarkdown) {
      setLoading(true)
      fetch(fileUrl)
        .then((res) => {
          if (!res.ok) throw new Error(`HTTP ${res.status}`)
          return res.text()
        })
        .then((text) => {
          setContent(text)
          setLoading(false)
        })
        .catch((err) => {
          setError(err.message)
          setLoading(false)
        })
    } else {
      setLoading(false)
    }
  }, [fileUrl, isImage, isText, isMarkdown])

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-2 border-b border-border flex-shrink-0">
        <span className="text-xs font-medium truncate flex-1 mr-2">{filename}</span>
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6"
            onClick={() => window.open(fileUrl, "_blank")}
          >
            <ExternalLink className="h-3 w-3" />
          </Button>
          <Button variant="ghost" size="icon" className="h-6 w-6" onClick={onClose}>
            <X className="h-3 w-3" />
          </Button>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-auto">
        {loading && (
          <div className="flex justify-center py-8">
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          </div>
        )}

        {error && (
          <div className="p-4 text-sm text-destructive">加载失败: {error}</div>
        )}

        {!loading && !error && isImage && (
          <div className="p-4 flex justify-center">
            <img
              src={fileUrl}
              alt={filename}
              className="max-w-full max-h-[400px] object-contain rounded"
            />
          </div>
        )}

        {!loading && !error && isMarkdown && (
          <div className="p-4 prose prose-sm dark:prose-invert max-w-none text-xs">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
          </div>
        )}

        {!loading && !error && isText && !isMarkdown && (
          <pre className="p-4 text-xs font-mono whitespace-pre-wrap break-all leading-relaxed">
            {content}
          </pre>
        )}

        {!loading && !error && !isImage && !isText && !isMarkdown && (
          <div className="p-4 text-center text-sm text-muted-foreground">
            <p className="mb-2">无法预览此文件格式</p>
            <Button variant="outline" size="sm" onClick={() => window.open(fileUrl, "_blank")}>
              <ExternalLink className="h-3.5 w-3.5 mr-1" />
              在新窗口打开
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}
