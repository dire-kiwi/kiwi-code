import { useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAppDispatch } from '@/store/hooks'
import { sidebarClosed, sidebarDismissed } from '@/store/slices/ui'

// Every destination the sidebar offers closes the sidebar behind it. Keeping
// that pairing in one place is what lets the sidebar navigate on its own instead
// of taking a callback per destination from App.
export function useSidebarNavigation() {
  const navigate = useNavigate()
  const dispatch = useAppDispatch()

  const navigateAndClose = useCallback((path: string) => {
    navigate(path)
    dispatch(sidebarClosed())
  }, [dispatch, navigate])

  // Selecting a thread also dismisses the finder, which the other destinations
  // do not: the finder is how you got here.
  const navigateAndDismiss = useCallback((path: string) => {
    navigate(path)
    dispatch(sidebarDismissed())
  }, [dispatch, navigate])

  return { navigateAndClose, navigateAndDismiss }
}
