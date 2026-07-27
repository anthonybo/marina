/**
 * Stops the harbour animating while nobody is looking at it.
 *
 * A running CSS animation prevents the browser from ever going idle: it keeps
 * producing frames at the display's refresh rate whether or not the result is
 * worth looking at. Measured on this machine, one perpetual animation costs
 * ~15% of a CPU core and the full harbour ~27%, against ~6% for the same page
 * held still. Chrome already stops animating a hidden tab, but not a visible
 * window that has lost focus — which is exactly how a dashboard is usually
 * left, parked on a second monitor while the work happens elsewhere.
 *
 * Pausing rather than cancelling keeps each animation's position, so returning
 * to the window resumes the scene instead of restarting it. Nothing here is
 * load-bearing: the harbour encodes all of its meaning in position and colour,
 * so a still scene reads exactly the same as a moving one.
 */
const ATTR = 'data-calm'

export function watchAttention(): () => void {
  const apply = () => {
    // document.hasFocus() is false for a background window even while visible,
    // which is the case Chrome's own throttling misses.
    const busy = document.hasFocus() && document.visibilityState === 'visible'
    if (busy) document.documentElement.removeAttribute(ATTR)
    else document.documentElement.setAttribute(ATTR, '')
  }

  apply()
  window.addEventListener('focus', apply)
  window.addEventListener('blur', apply)
  document.addEventListener('visibilitychange', apply)
  return () => {
    window.removeEventListener('focus', apply)
    window.removeEventListener('blur', apply)
    document.removeEventListener('visibilitychange', apply)
    document.documentElement.removeAttribute(ATTR)
  }
}
