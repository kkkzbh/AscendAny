import type * as Monaco from 'monaco-editor'
import type { DataCallback, Disposable as RpcDisposable, Message, MessageReader, MessageWriter } from 'vscode-jsonrpc'

type Disposable = { dispose: () => void }

let monacoServicesInstalled = false

function toWsOrigin(origin: string): string {
  const o = (origin || '').trim()
  if (!o) return ''
  if (/^wss?:\/\//i.test(o)) return o
  if (/^https:\/\//i.test(o)) return `wss://${o.slice('https://'.length)}`
  if (/^http:\/\//i.test(o)) return `ws://${o.slice('http://'.length)}`
  return o
}

function isLocalHostName(hostname: string): boolean {
  const h = (hostname || '').toLowerCase()
  return h === 'localhost' || h === '127.0.0.1' || h === '[::1]' || h === '::1'
}

function resolveBackendOrigin(raw: string): string {
  const value = (raw || '').toString().trim()
  if (!value) return ''
  try {
    const parsed = new URL(value)
    const pageHost = window.location.hostname || ''
    if (!isLocalHostName(pageHost) && isLocalHostName(parsed.hostname)) {
      return ''
    }
    if (window.location.protocol === 'https:' && parsed.protocol === 'http:') {
      return ''
    }
    return parsed.origin
  } catch {
    return ''
  }
}

function resolveWsUrl(path: string): string {
  const p = (path || '').startsWith('/') ? path : `/${path || ''}`
  const backend = resolveBackendOrigin((import.meta.env.VITE_BACKEND_ORIGIN || '').toString())
  const base = backend ? backend.replace(/\/+$/g, '') : window.location.origin
  let wsBase = toWsOrigin(base || window.location.origin)
  // Avoid mixed-content issues when the page is served over HTTPS.
  if (window.location.protocol === 'https:' && wsBase.startsWith('ws://')) {
    wsBase = `wss://${wsBase.slice('ws://'.length)}`
  }
  return `${wsBase}${p}`
}

export async function startCppClangdLsp(opts: {
  monaco: typeof Monaco
  accessToken: string
  onStatus?: (s: 'connecting' | 'ready' | 'closed' | 'error') => void
}): Promise<Disposable> {
  const { monaco, accessToken, onStatus } = opts
  onStatus?.('connecting')

  const withTimeout = async <T>(p: Promise<T>, ms: number, label: string): Promise<T> => {
    let t: ReturnType<typeof setTimeout> | null = null
    try {
      const timeoutP = new Promise<T>((_, reject) => {
        t = setTimeout(() => reject(new Error(`${label} timeout (${ms}ms)`)), ms)
      })
      return await Promise.race([p, timeoutP])
    } finally {
      if (t) {
        try {
          clearTimeout(t)
        } catch {
          // ignore
        }
      }
    }
  }

  // Important: monaco-languageclient (v0.x) pulls in vscode-languageclient, which imports
  // the `vscode` shim at module-evaluation time. That shim expects MonacoServices to be installed.
  const { MonacoServices } = await withTimeout(import('monaco-languageclient/lib/monaco-services'), 8000, 'load MonacoServices')
  if (!monacoServicesInstalled) {
    MonacoServices.install(monaco, { rootUri: 'file:///work' })
    monacoServicesInstalled = true
  }

  const [{ MonacoLanguageClient, CloseAction, ErrorAction, createConnection }, jsonrpc] = await withTimeout(
    Promise.all([import('monaco-languageclient'), import('vscode-jsonrpc')]),
    12000,
    'load LSP client',
  )

  const {
    AbstractMessageReader,
    AbstractMessageWriter,
    Disposable: JsonRpcDisposable,
    createMessageConnection,
  } = jsonrpc

  const url = resolveWsUrl(`/ws/oj/lsp/cpp/?access_token=${encodeURIComponent(accessToken)}`)
  const webSocket = new WebSocket(url)

  let stopped = false
  let languageClient: InstanceType<typeof MonacoLanguageClient> | null = null
  let connection: ReturnType<typeof createConnection> | null = null

  const close = () => {
    if (stopped) return
    stopped = true
    try {
      languageClient?.stop()
    } catch {
      // ignore
    }
    languageClient = null

    try {
      connection?.dispose()
    } catch {
      // ignore
    }
    connection = null

    try {
      webSocket.close()
    } catch {
      // ignore
    }
  }

  const ready = new Promise<void>((resolve, reject) => {
    let settled = false
    let opened = false
    let startTimer: ReturnType<typeof setTimeout> | null = null

    const cleanup = () => {
      if (startTimer) {
        try {
          clearTimeout(startTimer)
        } catch {
          // ignore
        }
        startTimer = null
      }
      try {
        webSocket.removeEventListener('open', onWsOpen)
        webSocket.removeEventListener('close', onWsClose)
        webSocket.removeEventListener('error', onWsError)
      } catch {
        // ignore
      }
    }

    const settleReject = (err: unknown) => {
      if (settled) return
      settled = true
      cleanup()
      reject(err instanceof Error ? err : new Error(String(err)))
    }

    const settleResolve = () => {
      if (settled) return
      settled = true
      cleanup()
      resolve()
    }

    const onWsError = () => {
      onStatus?.('error')
      close()
      settleReject(new Error('WebSocket error'))
    }

    const onWsClose = (ev: CloseEvent) => {
      onStatus?.('closed')
      close()
      settleReject(new Error(`WebSocket closed (code=${ev.code})`))
    }

    const onWsOpen = () => {
      if (opened) return
      opened = true
      try {
        class WebSocketJsonRpcReader extends AbstractMessageReader implements MessageReader {
          private state: 'initial' | 'listening' | 'closed' = 'initial'
          private callback: DataCallback | undefined
          private readonly queue: unknown[] = []

          constructor(private readonly ws: WebSocket) {
            super()
            this.ws.addEventListener('message', this.handleMessageEvent)
            this.ws.addEventListener('close', this.handleCloseEvent)
            this.ws.addEventListener('error', this.handleErrorEvent)
          }

          listen(callback: DataCallback): RpcDisposable {
            if (this.state === 'closed') {
              return JsonRpcDisposable.create(() => undefined)
            }
            this.state = 'listening'
            this.callback = callback
            while (this.queue.length > 0) {
              const item = this.queue.shift()
              void this.handleRaw(item)
            }
            return JsonRpcDisposable.create(() => {
              if (this.callback === callback) {
                this.callback = undefined
              }
            })
          }

          override dispose(): void {
            super.dispose()
            this.state = 'closed'
            this.callback = undefined
            this.queue.splice(0, this.queue.length)
            try {
              this.ws.removeEventListener('message', this.handleMessageEvent)
              this.ws.removeEventListener('close', this.handleCloseEvent)
              this.ws.removeEventListener('error', this.handleErrorEvent)
            } catch {
              // ignore
            }
          }

          private readonly handleMessageEvent = (event: MessageEvent) => {
            if (this.state !== 'listening') {
              this.queue.push(event.data)
              return
            }
            void this.handleRaw(event.data)
          }

          private readonly handleCloseEvent = () => {
            if (this.state === 'closed') return
            this.state = 'closed'
            this.fireClose()
          }

          private readonly handleErrorEvent = () => {
            if (this.state === 'closed') return
            this.fireError(new Error('WebSocket error'))
          }

          private async handleRaw(raw: unknown): Promise<void> {
            try {
              let text = ''
              if (typeof raw === 'string') {
                text = raw
              } else if (raw instanceof ArrayBuffer) {
                text = new TextDecoder().decode(new Uint8Array(raw))
              } else if (raw instanceof Blob) {
                text = await raw.text()
              } else {
                text = String(raw)
              }

              const data = JSON.parse(text) as Message
              this.callback?.(data)
            } catch (e) {
              this.fireError(e)
            }
          }
        }

        class WebSocketJsonRpcWriter extends AbstractMessageWriter implements MessageWriter {
          private errorCount = 0

          constructor(private readonly ws: WebSocket) {
            super()
          }

          async write(msg: Message): Promise<void> {
            try {
              const content = JSON.stringify(msg)
              this.ws.send(content)
            } catch (e) {
              this.errorCount += 1
              this.fireError(e, msg, this.errorCount)
              throw e
            }
          }

          end(): void {
            // no-op
          }
        }

        const reader = new WebSocketJsonRpcReader(webSocket)
        const writer = new WebSocketJsonRpcWriter(webSocket)
        const messageConnection = createMessageConnection(reader, writer)

        languageClient = new MonacoLanguageClient({
          name: 'C++ (clangd)',
          clientOptions: {
            documentSelector: ['cpp'],
            errorHandler: {
              error: () => ErrorAction.Continue,
              closed: () => CloseAction.DoNotRestart,
            },
            // clangd semantic tokens can be quite chatty; disabling reduces long-session pressure
            // while keeping completion + diagnostics.
            middleware: {
              provideDocumentSemanticTokens: () => null,
              provideDocumentSemanticTokensEdits: () => null,
              provideDocumentRangeSemanticTokens: () => null,
            },
          },
          connectionProvider: {
            get: async (errorHandler, closeHandler) => {
              const safeMessageConnection = messageConnection as unknown as Parameters<typeof createConnection>[0]
              connection = createConnection(safeMessageConnection, errorHandler, closeHandler)
              return connection
            },
          },
        })

        languageClient.start()

        void languageClient
          .onReady()
          .then(() => {
            onStatus?.('ready')
            settleResolve()
          })
          .catch((e: unknown) => {
            onStatus?.('error')
            close()
            settleReject(e)
          })
      } catch (e) {
        onStatus?.('error')
        close()
        settleReject(e)
      }
    }

    const START_TIMEOUT_MS = 12000
    startTimer = setTimeout(() => {
      onStatus?.('error')
      close()
      settleReject(new Error(`LSP init timeout (${START_TIMEOUT_MS}ms)`))
    }, START_TIMEOUT_MS)

    webSocket.addEventListener('open', onWsOpen)
    webSocket.addEventListener('close', onWsClose)
    webSocket.addEventListener('error', onWsError)

    // In case the socket is already open (very fast reconnect), start immediately.
    if (webSocket.readyState === WebSocket.OPEN) {
      onWsOpen()
    }
  })

  try {
    await ready
  } catch (e) {
    close()
    throw e
  }

  return { dispose: close }
}
