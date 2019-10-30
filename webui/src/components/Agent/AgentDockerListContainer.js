import React, { useEffect } from 'react'
import Panel from '../UI/Panel'
import AgentDockerList from './AgentDockerList'

const AgentDockerListContainer = () => {
  useEffect(() => {
    document.title = 'Docker | Glouton'
  }, [])
  return (
    <div style={{ marginTop: '1.5rem' }}>
      <Panel className="marginOffset">
        <h2>Docker</h2>
        <AgentDockerList />
      </Panel>
    </div>
  )
}

export default AgentDockerListContainer
