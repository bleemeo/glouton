var path = require('path')

exports.generateConfig = function(isDev) {
  return {
    entry: {
      'panel-glouton-main': ['@babel/polyfill', './src/index.js']
    },
    output: {
      filename: 'js/[name].js',
      path: path.resolve(__dirname, 'dist'),
      chunkFilename: 'js/[name].js'
    },
    module: {
      noParse: /node_modules\/localforage\/dist\/localforage.js/,
      rules: [
        {
          test: /\.(scss)$/,
          use: ['style-loader', 'css-loader', 'sass-loader']
        },
        { test: /(\.css$)/, loaders: ['style-loader', 'css-loader'] },
        { test: /\.(woff|woff2|eot|ttf|svg)$/, loader: 'url-loader?limit=100000' },
        {
          test: /\.js$/,
          exclude: /(node_modules)/,
          use: {
            loader: 'babel-loader'
          }
        }
      ]
    }
  }
}
