export default function WelcomePage() {
  return (
    <div className="flex-1 flex items-center justify-center">
      <div className="text-center space-y-4">
        <div className="text-6xl">🐢</div>
        <h1 className="text-2xl font-semibold">SeaTurt</h1>
        <p className="text-muted-foreground text-sm max-w-md">
          选择左侧的 Agent 开始对话，或点击「新建 Agent」创建一个。
        </p>
      </div>
    </div>
  )
}
