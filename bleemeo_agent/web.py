import threading
import time

import flask
import requests

app = flask.Flask(__name__)
app_thread = threading.Thread(target=app.run)


@app.route('/')
def home():
    return flask.render_template('index.html', agent=app.agent)


@app.route('/admin', methods=['GET', 'POST'])
def admin():
    if flask.request.method == 'POST':
        action = flask.request.form.get('action')
        if action == 'refresh_plugins':
            if app.agent.reload_plugins():
                return flask.render_template(
                    'restarting.html', agent=app.agent)
            else:
                flask.flash('Re-scan finished. List unchanged')
        elif action.startswith('restore-'):
            check_index0 = int(action[len('restore-'):])
            if check_index0 < len(app.agent.check_thread.checks):
                check = app.agent.check_thread.checks[check_index0]
                check.fake_failure_stop()
        elif action.startswith('fake-failure-'):
            check_index0 = int(action[len('fake-failure-'):])
            if check_index0 < len(app.agent.check_thread.checks):
                check = app.agent.check_thread.checks[check_index0]
                check.fake_failure_start()

    return flask.render_template(
        'admin.html', agent=app.agent, now=time.time())


@app.route('/about')
def about():
    return flask.render_template('about.html', agent=app.agent)


@app.route('/_quit')
def quit():
    # "internal" request endpoint. Used to stop Web thread.
    # We need to stop web-thread during reload/re-exec (or else, the port will
    # be already used).
    # I didn't find better way to stop a flask application... we need to be
    # during a request processing to access "werkzeug.server.shutdown" :/
    # So when agent want to shutdown, it need to do one request to this URL.
    if not app.agent.is_terminating.is_set():
        # hum... agent is not stopping...
        # Since this endpoint is "public", maybe someone is trying to
        # mess with us, just ignore the request
        return flask.redirect(flask.url_for('home'))

    func = flask.request.environ.get('werkzeug.server.shutdown')
    if func is None:
        raise RuntimeError('Not running with the Werkzeug Server')
    func()
    return 'Shutdown in progress...'


def start_server(agent):
    app.agent = agent
    app.secret_key = agent.generated_values['secret_key']
    app_thread.daemon = True
    app_thread.start()


def shutdown_server():
    requests.get('http://localhost:5000/_quit')
    app_thread.join()
