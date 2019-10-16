import React from 'react'
import PropTypes from 'prop-types'

/*
  Several articles said that <a> should only be used to link to another page
  and that we should use <button> instead for SPA link or action link.
  But if we use a <button> a lot of style gets broken, event with .btn-link
  from bootstrap. So we use <a> with tab-index="0" and a special onKeyPress
  handler to have the same behavior than with a button, aka onClick is
  triggered when Enter or Space is pressed.
 */

const handleKeyPress = (event, onClick) => {
  // simulate a click when the user press Enter or Space with the focus on the link
  if (event.charCode === 13 || event.charCode === 32) {
    onClick(event)
  }
  // TODO should we do smth here
}

const A = props => {
  const { className, onClick, children, ...btnProps } = props
  const onKeyPress = handleKeyPress
  return (
    <a
      tabIndex="0"
      className={`${className ? className : ''}`}
      onClick={onClick}
      onKeyPress={e => onKeyPress(e, onClick)}
      {...btnProps}
    >
      {children}
    </a>
  )
}

A.propTypes = {
  className: PropTypes.string,
  onClick: PropTypes.func,
  children: PropTypes.object
}

export default A
