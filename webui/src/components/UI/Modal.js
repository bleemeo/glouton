import classnames from 'classnames'
import { Modal as ModalStrap, ModalHeader, ModalBody, ModalFooter } from 'reactstrap'
import React from 'react'
import PropTypes from 'prop-types'

export default class Modal extends React.Component {
  static propTypes = {
    title: PropTypes.oneOfType([PropTypes.string, PropTypes.element]).isRequired,
    disabledMainBtn: PropTypes.bool,
    disabledCloseBtn: PropTypes.bool,
    mainBtnLabel: PropTypes.string,
    mainBtnAction: PropTypes.func,
    closeLabel: PropTypes.string,
    closeAction: PropTypes.func,
    children: PropTypes.oneOfType([PropTypes.object.isRequired, PropTypes.array.isRequired]).isRequired,
    mainBtnActionType: PropTypes.string,
    className: PropTypes.string,
    size: PropTypes.string
  }

  static defaultProps = {
    disabledMainBtn: false,
    disabledCloseBtn: false,
    closeOnBackdropClick: true,
    larger: false,
    className: ''
  }

  constructor(props) {
    super(props)
    this.state = {
      isOpen: false
    }

    this.toggle = this.handleToggle.bind(this)
  }

  componentDidMount() {
    this.handleToggle()
  }

  handleToggle() {
    this.setState({ isOpen: !this.state.isOpen }, () => {
      if (!this.state.isOpen) this.props.closeAction()
    })
  }

  render() {
    const {
      disabledMainBtn,
      disabledCloseBtn,
      title,
      mainBtnLabel,
      mainBtnAction,
      closeLabel,
      mainBtnActionType,
      className,
      children,
      size
    } = this.props

    let mainBtnClassName = null
    if (mainBtnActionType && mainBtnActionType === 'delete') {
      mainBtnClassName = 'btn-danger'
    } else {
      mainBtnClassName = 'btn-primary'
    }

    let mainBtn = null
    if (mainBtnAction && mainBtnLabel) {
      mainBtn = (
        <button type="button" className={'btn ' + mainBtnClassName} onClick={mainBtnAction} disabled={disabledMainBtn}>
          {mainBtnLabel}
        </button>
      )
    }

    let footer = null
    if (mainBtn || closeLabel) {
      footer = (
        <ModalFooter>
          <button
            type="button"
            className="btn btn-secondary"
            data-dismiss="modal"
            onClick={this.handleToggle}
            disabled={disabledCloseBtn}
          >
            {closeLabel}
          </button>
          {mainBtn}
        </ModalFooter>
      )
    }

    return (
      <ModalStrap
        isOpen={this.state.isOpen}
        oggle={() => this.handleToggle()}
        className={classnames(`modal-dialog ${className}`)}
        size={size}
      >
        <ModalHeader toggle={() => this.handleToggle()}>
          <span className="modal-title" id="myModalLabel">
            {typeof title === 'string' ? <h4>{title}</h4> : title}
          </span>
        </ModalHeader>
        <ModalBody>{children}</ModalBody>
        {footer}
      </ModalStrap>
    )
  }
}
