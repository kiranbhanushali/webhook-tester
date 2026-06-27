import { Button, Modal, PasswordInput, Stack, Text } from '@mantine/core'
import React, { createContext, useCallback, useContext, useEffect, useState } from 'react'
import { type Client, clearStoredToken, getStoredToken, onUnauthorized, setStoredToken } from '~/api'

type AuthContextType = {
  /** True when the server has rejected a request with 401 and a token is required to continue. */
  readonly authRequired: boolean

  /** The currently configured token (or null when none is set). */
  readonly token: string | null

  /**
   * Log in with a shared token: POSTs `/api/auth/login` (so the WebSocket cookie is set), and on success persists the
   * token for the Bearer middleware. Returns true on success, false when the token is rejected.
   */
  login(token: string): Promise<boolean>

  /** Clear the stored token. */
  logout(): void
}

const notInitialized = (): never => {
  throw new Error('AuthProvider is not initialized')
}

const authContext = createContext<AuthContextType>({
  authRequired: false,
  token: null,
  login: () => notInitialized(),
  logout: () => notInitialized(),
})

/**
 * AuthProvider wires the shared-token auth plumbing into the UI:
 *
 *   - it exposes {@link AuthContextType.login}/{@link AuthContextType.logout} and the current token, and
 *   - it listens for server 401 responses (via the api auth bus) and surfaces a minimal token prompt so the app stays
 *     usable when auth is enabled. A richer login UI can be layered on by a later task.
 */
export const AuthProvider: React.FC<{ api: Client; children: React.ReactNode }> = ({ api, children }) => {
  const [token, setToken] = useState<string | null>(() => getStoredToken())
  const [authRequired, setAuthRequired] = useState<boolean>(false)
  const [inputToken, setInputToken] = useState<string>('')
  const [submitting, setSubmitting] = useState<boolean>(false)
  const [error, setError] = useState<string | null>(null)

  // surface server 401s as a token prompt (onUnauthorized returns its own unsubscribe function)
  useEffect(() => onUnauthorized(() => setAuthRequired(true)), [])

  const login = useCallback<AuthContextType['login']>(
    async (t) => {
      const ok = await api.login(t)

      if (ok) {
        setStoredToken(t)
        setToken(t)
        setAuthRequired(false)
      }

      return ok
    },
    [api]
  )

  const logout = useCallback<AuthContextType['logout']>(() => {
    clearStoredToken()
    setToken(null)
  }, [])

  const handleSubmit = useCallback(
    async (e: React.FormEvent): Promise<void> => {
      e.preventDefault()
      setError(null)
      setSubmitting(true)

      try {
        const ok = await login(inputToken)

        if (ok) {
          setInputToken('')
        } else {
          setError('Invalid token. Please try again.')
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      } finally {
        setSubmitting(false)
      }
    },
    [login, inputToken]
  )

  return (
    <authContext.Provider value={{ authRequired, token, login, logout }}>
      {children}
      <Modal
        opened={authRequired}
        onClose={() => setAuthRequired(false)}
        title="Authentication required"
        centered
        withCloseButton
      >
        <form onSubmit={handleSubmit}>
          <Stack>
            <Text size="sm" c="dimmed">
              This server requires an access token. Enter it below to continue.
            </Text>
            <PasswordInput
              label="Access token"
              placeholder="Paste your token"
              value={inputToken}
              onChange={(ev) => setInputToken(ev.currentTarget.value)}
              error={error}
              data-autofocus
            />
            <Button type="submit" loading={submitting} disabled={!inputToken}>
              Sign in
            </Button>
          </Stack>
        </form>
      </Modal>
    </authContext.Provider>
  )
}

export function useAuth(): AuthContextType {
  const ctx = useContext(authContext)

  if (!ctx) {
    throw new Error('useAuth must be used within an AuthProvider')
  }

  return ctx
}
