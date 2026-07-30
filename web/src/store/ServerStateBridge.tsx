import { useEffect } from 'react'
import { useAppDispatch } from '@/store/hooks'
import { activitiesReceived } from '@/store/slices/agentActivity'
import { profilesReceived } from '@/store/slices/profiles'
import { projectsReceived } from '@/store/slices/projects'
import { settingsFailed, settingsLoading, settingsReceived } from '@/store/slices/settings'
import type { AppSettings, PiThreadActivity, Profile, Project } from '@/types'
import { useSubscription } from '@/wire/react'
import {
  AgentActivityTopic,
  ProfilesTopic,
  ProjectsTopic,
  SettingsTopic,
} from '@/wire/topics'

// Renders nothing. Its whole job is to be the one place a server topic is
// copied into the store, so no component has to unwrap a subscription just to
// read shared data.
//
// It sits above ThemeProvider in main.tsx because ThemeProvider is the topmost
// settings reader; anything lower would leave the theme a render behind.
//
// App still opens its own subscriptions to these same topics. That is not a
// duplicate fetch -- the client keys channels by topic and params, so both share
// one -- and App needs the subscription objects themselves for the connection
// banner and its retry button, which is transport state rather than data.
export function ServerStateBridge() {
  const dispatch = useAppDispatch()
  const settings = useSubscription(SettingsTopic, undefined)
  const projects = useSubscription(ProjectsTopic, undefined)
  const profiles = useSubscription(ProfilesTopic, undefined)
  const activity = useSubscription(AgentActivityTopic, undefined)

  useEffect(() => {
    if (settings.state === 'loading') {
      dispatch(settingsLoading())
      return
    }
    if (settings.state === 'error') {
      dispatch(settingsFailed(settings.error.message))
      return
    }
    dispatch(settingsReceived(settings.data as AppSettings))
  }, [dispatch, settings])

  useEffect(() => {
    if (projects.state !== 'ready') return
    dispatch(projectsReceived(projects.data as Project[]))
  }, [dispatch, projects])

  useEffect(() => {
    if (profiles.state !== 'ready') return
    dispatch(profilesReceived(profiles.data as Profile[]))
  }, [dispatch, profiles])

  useEffect(() => {
    if (activity.state !== 'ready') return
    dispatch(activitiesReceived(activity.data as PiThreadActivity[]))
  }, [activity, dispatch])

  return null
}
