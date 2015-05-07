import threading

import flask
app = flask.Flask(__name__)


@app.route('/')
def hello_world():
    return flask.render_template('index.html', agent=app.agent)


def start_server(agent):
    app.agent = agent
    t = threading.Thread(target=app.run)
    t.daemon = True
    t.start()
