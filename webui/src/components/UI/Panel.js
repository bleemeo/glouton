import React from 'react'
import { Grid, Card } from 'tabler-react'
import PropTypes from 'prop-types'

const Panel = ({ children, className = '', xl = 12, md = 12, offset = 0, noBorder = false }) => (
  <Grid.Row>
    <Grid.Col xl={xl} md={md} offset={offset}>
      <Card className={noBorder ? 'no-border' : null}>
        <div style={{ marginTop: '-1.8rem' }}>
          <Card.Body>
            <div className={className}>{children}</div>
          </Card.Body>
        </div>
      </Card>
    </Grid.Col>
  </Grid.Row>
)

Panel.propTypes = {
  children: PropTypes.any.isRequired,
  className: PropTypes.string,
  xl: PropTypes.number,
  md: PropTypes.number,
  offset: PropTypes.number,
  noBorder: PropTypes.bool
}

export default Panel
