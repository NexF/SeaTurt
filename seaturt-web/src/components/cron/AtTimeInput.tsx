import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"

interface Props {
  value: string // ISO datetime-local format: "2026-03-10T09:00"
  onChange: (value: string) => void
  error?: string
}

export default function AtTimeInput({ value, onChange, error }: Props) {
  // Get minimum datetime (now + 1 minute, in local datetime-local format)
  const now = new Date()
  now.setMinutes(now.getMinutes() + 1, 0, 0)
  const minValue = toDatetimeLocal(now)

  return (
    <div className="space-y-2">
      <Label>执行时间</Label>
      <Input
        type="datetime-local"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        min={minValue}
        className={error ? "border-destructive" : ""}
      />
      {error && <p className="text-xs text-destructive">{error}</p>}
      <p className="text-[11px] text-muted-foreground">指定一个未来时间点，到时执行一次后自动禁用</p>
    </div>
  )
}

function toDatetimeLocal(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}
