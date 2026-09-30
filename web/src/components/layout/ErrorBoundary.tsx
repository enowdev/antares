import { Component, type ErrorInfo, type ReactNode } from 'react'
import { ArrowClockwise, Warning } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/primitives'

interface Props {
  children: ReactNode
  /** Changing this resets the boundary — pass the route so navigation recovers. */
  resetKey?: string
  labels: {
    title: string
    description: string
    retry: string
  }
}

interface State {
  error: Error | null
}

/**
 * Keeps a crash inside one page from blanking the whole dashboard. React has no
 * hook equivalent, so this stays a class component.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidUpdate(prev: Props) {
    // A new route means a new page; clear the previous failure.
    if (prev.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null })
    }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Antares: page crashed', error, info.componentStack)
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children

    return (
      <Card reveal={false} className="m-rise border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))]">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-destructive">
            <Warning className="size-4 shrink-0" weight="fill" />
            {this.props.labels.title}
          </CardTitle>
          <CardDescription>{this.props.labels.description}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words border border-border bg-background p-3 font-mono text-[11px] text-muted-foreground">
            {error.message}
          </pre>
          <Button size="sm" variant="outline" onClick={() => this.setState({ error: null })} className="gap-1.5">
            <ArrowClockwise className="size-4" />
            {this.props.labels.retry}
          </Button>
        </CardContent>
      </Card>
    )
  }
}
