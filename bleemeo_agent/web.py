import threading

import flask
import requests

import bleemeo_agent.checker


app = flask.Flask(__name__)
app_thread = threading.Thread(target=app.run)


@app.route('/')
def home():
    loads = app.core.get_loads()

    return flask.render_template(
        'index.html', core=app.core, loads=' '.join(loads))


@app.route('/_quit')
def quit():
    # "internal" request endpoint. Used to stop Web thread.
    # We need to stop web-thread during reload/re-exec (or else, the port will
    # be already used).
    # I didn't find better way to stop a flask application... we need to be
    # during a request processing to access "werkzeug.server.shutdown" :/
    # So when agent want to shutdown, it need to do one request to this URL.
    if not app.core.is_terminating.is_set():
        # hum... agent is not stopping...
        # Since this endpoint is "public", maybe someone is trying to
        # mess with us, just ignore the request
        return flask.redirect(flask.url_for('home'))

    func = flask.request.environ.get('werkzeug.server.shutdown')
    if func is None:
        raise RuntimeError('Not running with the Werkzeug Server')
    func()
    return 'Shutdown in progress...'


def start_server(core):
    app.core = core
    if app.core.stored_values.get('web_secret_key') is None:
        app.core.stored_values.set(
            'web_secret_key', bleemeo_agent.util.generate_password())
    app.secret_key = app.core.stored_values.get('web_secret_key')
    app_thread.daemon = True
    app_thread.start()


def shutdown_server():
    requests.get('http://localhost:5000/_quit')
    app_thread.join()
