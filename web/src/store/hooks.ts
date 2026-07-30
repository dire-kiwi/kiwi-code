import { useDispatch, useSelector, useStore } from 'react-redux'
import type { AppDispatch, AppStore } from './index'
import type { RootState } from './rootReducer'

export const useAppDispatch = useDispatch.withTypes<AppDispatch>()
export const useAppSelector = useSelector.withTypes<RootState>()
// For state that must be read exactly once, outside the subscription.
export const useAppStore = useStore.withTypes<AppStore>()
