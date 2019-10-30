import React from 'react'
import { ApolloProvider } from '@apollo/react-hooks'
import AgentContainer from './Agent/AgentContainer'
import TopNavBar from './App/TopNavBar'
import client from '../utils/API'

const Root = () => {
  return (
    <ApolloProvider client={client}>
      <div className="marginOffset">
        <TopNavBar />
        <div className="main-content">
          <AgentContainer />
        </div>
      </div>
    </ApolloProvider>
  )
}

export default Root
