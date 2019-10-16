import React, { lazy, Suspense } from 'react'
import ReactDom from 'react-dom'
import PanelErrorBoundary from './components/UI/PanelErrorBoundary'
import PanelLoading from './components/UI/PanelLoading'

import './styles/bootstrap.scss'
import 'tabler-react/dist/Tabler.css'

const App = lazy(() => import('./components/Root'))

ReactDom.render(
  <PanelErrorBoundary>
    <Suspense fallback={<PanelLoading />}>
      <App />
    </Suspense>
  </PanelErrorBoundary>,
  document.getElementById('main')
)
