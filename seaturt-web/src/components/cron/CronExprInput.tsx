import { useState, useEffect, useCallback } from "react"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

const PRESETS = [
  { label: "每小时", expr: "0 * * * *" },
  { label: "每天 9:00", expr: "0 9 * * *" },
  { label: "每天 18:00", expr: "0 18 * * *" },
  { label: "每周一 9:00", expr: "0 9 * * 1" },
  { label: "每月 1 号", expr: "0 0 1 * *" },
]

interface Props {
  value: string
  onChange: (value: string) => void
  error?: string
}

/** Simple cron-expression next-run calculator (client-side, 5-field only) */
function nextCronTimes(expr: string, count: number): Date[] | null {
  const parts = expr.trim().split(/\s+/)
  if (parts.length !== 5) return null

  const parseField = (field: string, min: number, max: number): number[] | null => {
    const values: number[] = []
    for (const part of field.split(",")) {
      if (part === "*") {
        for (let i = min; i <= max; i++) values.push(i)
      } else if (part.includes("/")) {
        const [base, stepStr] = part.split("/")
        const step = parseInt(stepStr)
        if (isNaN(step) || step <= 0) return null
        const start = base === "*" ? min : parseInt(base)
        if (isNaN(start)) return null
        for (let i = start; i <= max; i += step) values.push(i)
      } else if (part.includes("-")) {
        const [a, b] = part.split("-").map(Number)
        if (isNaN(a) || isNaN(b)) return null
        for (let i = a; i <= b; i++) values.push(i)
      } else {
        const n = parseInt(part)
        if (isNaN(n) || n < min || n > max) return null
        values.push(n)
      }
    }
    return values.length > 0 ? values : null
  }

  const minutes = parseField(parts[0], 0, 59)
  const hours = parseField(parts[1], 0, 23)
  const doms = parseField(parts[2], 1, 31)
  const months = parseField(parts[3], 1, 12)
  const dows = parseField(parts[4], 0, 6)

  if (!minutes || !hours || !doms || !months || !dows) return null

  const results: Date[] = []
  const now = new Date()
  const cursor = new Date(now)
  cursor.setSeconds(0, 0)
  cursor.setMinutes(cursor.getMinutes() + 1)

  const maxIter = 525600 // max 1 year of minutes
  for (let i = 0; i < maxIter && results.length < count; i++) {
    if (
      months.includes(cursor.getMonth() + 1) &&
      hours.includes(cursor.getHours()) &&
      minutes.includes(cursor.getMinutes()) &&
      dows.includes(cursor.getDay()) &&
      doms.includes(cursor.getDate())
    ) {
      results.push(new Date(cursor))
    }
    cursor.setMinutes(cursor.getMinutes() + 1)
  }
  return results.length > 0 ? results : null
}

function formatDateTime(d: Date): string {
  const days = ["日", "一", "二", "三", "四", "五", "六"]
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} (周${days[d.getDay()]}) ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export default function CronExprInput({ value, onChange, error }: Props) {
  const [preview, setPreview] = useState<string[] | null>(null)

  const computePreview = useCallback((expr: string) => {
    if (!expr.trim()) {
      setPreview(null)
      return
    }
    const times = nextCronTimes(expr, 5)
    if (times) {
      setPreview(times.map(formatDateTime))
    } else {
      setPreview(null)
    }
  }, [])

  useEffect(() => {
    computePreview(value)
  }, [value, computePreview])

  return (
    <div className="space-y-2">
      <Label>Cron 表达式</Label>
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="分 时 日 月 周（如 0 9 * * *）"
        className={`font-mono ${error ? "border-destructive" : ""}`}
      />
      {error && <p className="text-xs text-destructive">{error}</p>}
      <div className="flex flex-wrap gap-1.5">
        {PRESETS.map((p) => (
          <button
            key={p.expr}
            type="button"
            onClick={() => onChange(p.expr)}
            className={`text-xs px-2.5 py-1 rounded-full border transition-colors cursor-pointer ${
              value === p.expr
                ? "bg-primary text-primary-foreground border-primary"
                : "border-border text-muted-foreground hover:bg-accent hover:text-foreground hover:border-ring"
            }`}
          >
            {p.label}
          </button>
        ))}
      </div>
      {preview && preview.length > 0 && (
        <div className="bg-background border border-border rounded-md p-2.5 mt-2">
          <p className="text-[11px] text-muted-foreground font-medium mb-1.5">接下来 5 次执行时间</p>
          <ul className="space-y-0.5">
            {preview.map((t, i) => (
              <li key={i} className="text-xs font-mono flex items-center gap-1.5">
                <span className="w-1 h-1 rounded-full bg-primary flex-shrink-0" />
                {t}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
