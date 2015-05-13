import threading

import flask
app = flask.Flask(__name__)


@app.route('/')
def home():
    return flask.render_template('index.html', agent=app.agent)

@app.route('/about')
def about():
    return flask.render_template('about.html', agent=app.agent)


def start_server(agent):
    app.agent = agent
    t = threading.Thread(target=app.run)
    t.daemon = True
    t.start()
