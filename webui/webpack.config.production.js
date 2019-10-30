var webpack = require('webpack')
var path = require('path')

var webpackConfig = require('./webpack.config')

var config = webpackConfig.generateConfig(false)

const buildTimestamp = new Date().getTime()

config.devtool = 'source-map'
config.mode = 'production'
config.output.path = path.join(__dirname, 'dist')
config.output.filename = `js/[name].js?ts=${buildTimestamp}`
config.output.chunkFilename = `js/[name].${buildTimestamp}.js`

config.plugins = [new webpack.IgnorePlugin(/^(redux-logger)(\/.+)?$/)]

module.exports = config
