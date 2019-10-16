var path = require('path')
var webpack = require('webpack')

var webpackConfig = require('./webpack.config')

var config = webpackConfig.generateConfig(true)

var host = 'localhost'
var port = 3015

config.devtool = 'cheap-module-source-map'
config.mode = 'development'
config.output.path = path.join(__dirname, 'dist')
config.devServer = {
  contentBase: config.output.path,
  port: port,
  compress: true,
  hot: true
}
config.output.publicPath = 'http://' + host + ':' + port + '/'

config.plugins = [new webpack.HotModuleReplacementPlugin()]

module.exports = config
