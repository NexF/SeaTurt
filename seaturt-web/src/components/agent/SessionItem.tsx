import { useState, useRef, useEffect } from "react"
import { MessageSquare, Trash2, Pencil } from "lucide-react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Session } from "@/types"
import { useAgentStore } from "@/stores/agentStore"
import { cn } from "@/lib/utils"

interface Props {
  session: Session
  selected: boolean
  onClick: () => void
}

export default function SessionItem({ session, selected, onClick }: Props) {
  const { deleteSession, renameSession } = useAgentStore()
  const [editing, setEditing] = useState(false)
  const [title, setTitle] = useState(session.title)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (editing) {
      inputRef.current?.focus()
      inputRef.current?.select()
    }
  }, [editing])

  const handleRename = () => {
    const trimmed = title.trim()
    if (trimmed && trimmed !== session.title) {
      renameSession(session.agent_id, session.id, trimmed)
    } else {
      setTitle(session.title)
    }
    setEditing(false)
  }

  const handleDelete = (e: React.MouseEvent) => {
    e.stopPropagation()
    setDeleteOpen(true)
  }

  const confirmDeleteSession = () => {
    deleteSession(session.agent_id, session.id)
    setDeleteOpen(false)
  }

  return (
    <>
    <div
      onClick={onClick}
      className={cn(
        "group flex items-center gap-2 pl-7 pr-2 py-1.5 rounded-md cursor-pointer text-sm transition-colors",
        "hover:bg-accent/50",
        selected && "bg-accent"
      )}
    >
      <MessageSquare className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />

      {editing ? (
        <input
          ref={inputRef}
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          onBlur={handleRename}
          onKeyDown={(e) => {
            if (e.key === "Enter") handleRename()
            if (e.key === "Escape") {
              setTitle(session.title)
              setEditing(false)
            }
          }}
          onClick={(e) => e.stopPropagation()}
          className="flex-1 min-w-0 bg-transparent border-b border-primary text-sm outline-none"
        />
      ) : (
        <span className="flex-1 truncate text-sm">{session.title}</span>
      )}

      {/* Action buttons — visible on hover */}
      {!editing && (
        <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
          <button
            onClick={(e) => {
              e.stopPropagation()
              setEditing(true)
            }}
            className="p-0.5 rounded hover:bg-accent text-muted-foreground hover:text-foreground"
          >
            <Pencil className="h-3 w-3" />
          </button>
          <button
            onClick={handleDelete}
            className="p-0.5 rounded hover:bg-accent text-muted-foreground hover:text-destructive"
          >
            <Trash2 className="h-3 w-3" />
          </button>
        </div>
      )}
    </div>

    <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>确认删除</AlertDialogTitle>
          <AlertDialogDescription>
            确定要删除对话「{session.title}」吗？此操作不可撤销。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction onClick={confirmDeleteSession} className="bg-destructive text-destructive-foreground">
            删除
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
    </>
  )
}
