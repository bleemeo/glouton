export const cssClassForStatus = status => {
  switch (status) {
    case 0:
    case 'OK':
      return 'success'
    case 1:
    case 'WARNING':
      return 'warning'
    case 2:
    case 'CRITICAL':
      return 'danger'
    // STATUS_UNKNOWN = 3
    case 10:
    case 'INFO':
      return 'info'
    default:
      return 'success' // or 'info' ??
  }
}

export const cssColorClassForStatus = status => {
  switch (status) {
    case 0:
    case 'OK':
      return 'green'
    case 1:
    case 'WARNING':
      return 'orange'
    case 2:
    case 'CRITICAL':
      return 'red'
    // STATUS_UNKNOWN = 3
    case 10:
    default:
      return 'blue' // or 'info' ??
  }
}

// TODO to remove once the API branch PRODUCT-101 has been merge into master
export const textForStatus = status => {
  switch (status) {
    case 0:
      return 'OK'
    case 1:
      return 'WARNING'
    case 2:
      return 'CRITICAL'
    // STATUS_UNKNOWN = 3
    case 10:
      return 'INFO'
    default:
      return 'OK'
  }
}

export const colorForStatus = status => {
  switch (status) {
    case 0:
      return '28A745'
    case 1:
      return 'f0ad4e'
    case 2:
      return 'd9534f'
    // STATUS_UNKNOWN = 3
    default:
      return '28A745'
  }
}

export const chartColorForStatus = status => {
  switch (status) {
    case 0:
      return '#5cb85c'
    case 1:
      return '#f0ad4e'
    case 2:
      return '#d9534f'
    case 3:
      return '#0275d8'
    case 10:
      return '#5bc0de'
  }
}

export const lightenDarkenColor = (color, percent) => {
  const f = color[0] === '#' ? parseInt(color.slice(1), 16) : parseInt(color, 16)
  const t = percent < 0 ? 0 : 255
  const p = percent < 0 ? percent * -1 : percent
  const R = f >> 16
  const G = (f >> 8) & 0x00ff
  const B = f & 0x0000ff
  return (
    '#' +
    (
      0x1000000 +
      (Math.round((t - R) * p) + R) * 0x10000 +
      (Math.round((t - G) * p) + G) * 0x100 +
      (Math.round((t - B) * p) + B)
    )
      .toString(16)
      .slice(1)
  )
}

export const convertURLToObjectParams = (url: string): Object => {
  const result = {}
  const getParams = url.split('?')[1]
  if (getParams) {
    getParams.split('&').map(param => {
      const key = param.split('=')[0]
      const value = param.split('=')[1]
      result[key] = value
    })
  }
  return result
}

export const statusColors = ['#60B822', '#FC964F', '#CA2B2D']
