'use strict';
// taken fom https://github.com/antelle/base64-loader and modified
module.exports = function(content) {
  this.cacheable && this.cacheable();
  var extension = this.resource.slice(this.resource.lastIndexOf('.')+1)
  return 'module.exports = "data:image/' + extension + ';base64,' + content.toString('base64') + '"';
};

module.exports.raw = true;