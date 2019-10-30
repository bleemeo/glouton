import React from 'react'

import DonutPieChart from '../UI/DonutPieChart'

import { colorForStatus } from '../utils/converter'
import { percentToString2Digits, formatToBytes, formatToBits } from '../utils/formater'

export const renderDonut = (name, value, status) => (
  <div className="small-widget">
    <div className="content">
      <DonutPieChart values={[{ value, color: colorForStatus(status) }]} valueFormatter={percentToString2Digits} />
    </div>
    <div className="title">{name}</div>
  </div>
)

export const renderNetwork = (name, sentValue, recvValue) => {
  const formattedSentValue = sentValue !== null ? formatToBits(sentValue) : null
  const formattedRecvValue = recvValue !== null ? formatToBits(recvValue) : null
  if (!formattedSentValue && !formattedRecvValue) {
    return (
      <div className="small-widget">
        <div className="content wide">
          <div className="content-row">
            <p style={{ fontSize: '80%', paddingTop: '15%' }}>N/A</p>
          </div>
        </div>
        <div className="title">{name}</div>
      </div>
    )
  } else {
    return (
      <div className="small-widget">
        <div className="content wide">
          <div className="content-row">
            {formattedSentValue ? (
              <span>
                {formattedSentValue[0]}
                <small>
                  &nbsp;
                  {formattedSentValue[1]}
                  /s sent
                </small>
              </span>
            ) : null}
          </div>
          <div className="content-row">
            {formattedRecvValue ? (
              <span>
                {formattedRecvValue[0]}
                <small>
                  &nbsp;
                  {formattedRecvValue[1]}
                  /s receive
                </small>
              </span>
            ) : null}
          </div>
        </div>
        <div className="title">{name}</div>
      </div>
    )
  }
}

export const renderDisk = (name, writeValue, readValue) => {
  const formattedWriteValue = writeValue !== null ? formatToBytes(writeValue) : null
  const formattedReadValue = readValue !== null ? formatToBytes(readValue) : null
  if (!formattedReadValue && !formattedWriteValue) {
    return (
      <div className="small-widget">
        <div className="content wide">
          <div className="content-row">
            <p style={{ fontSize: '80%', paddingTop: '15%' }}>N/A</p>
          </div>
        </div>
        <div className="title">{name}</div>
      </div>
    )
  } else {
    return (
      <div className="small-widget">
        <div className="content wide">
          <div className="content-row">
            {formattedWriteValue !== null ? (
              <span>
                {formattedWriteValue[0]}
                <small>
                  &nbsp;
                  {formattedWriteValue[1]}
                  /s write
                </small>
              </span>
            ) : null}
          </div>
          <div className="content-row">
            {formattedReadValue !== null ? (
              <span>
                {formattedReadValue[0]}
                <small>
                  &nbsp;
                  {formattedReadValue[1]}
                  /s read
                </small>
              </span>
            ) : null}
          </div>
        </div>
        <div className="title">{name}</div>
      </div>
    )
  }
}
