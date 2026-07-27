import { useCallback, useEffect, useState } from 'react'

/**
 * The whole router. Two routes — the dashboard and the terminals — need history
 * and a back button, not a routing library.
 */
export interface Route {
  name: 'dashboard' | 'logs' | 'health'
  /** For 'logs', the terminal being viewed, if any. */
  param: string | null
}

function parse(pathname: string): Route {
  const parts = pathname.replace(/^\/+|\/+$/g, '').split('/')
  if (parts[0] === 'logs') {
    return { name: 'logs', param: parts[1] ? decodeURIComponent(parts[1]) : null }
  }
  if (parts[0] === 'health') {
    return { name: 'health', param: null }
  }
  return { name: 'dashboard', param: null }
}

export function useRoute() {
  const [route, setRoute] = useState<Route>(() => parse(window.location.pathname))

  useEffect(() => {
    const onPop = () => setRoute(parse(window.location.pathname))
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  const navigate = useCallback((to: string) => {
    if (to !== window.location.pathname) {
      window.history.pushState(null, '', to)
    }
    setRoute(parse(to))
  }, [])

  return { route, navigate }
}
