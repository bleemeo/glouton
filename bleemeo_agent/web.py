import threading
import time

import flask
import requests

import bleemeo_agent.checker


app = flask.Flask(__name__)
app_thread = threading.Thread(target=app.run)


@app.route('/')
def home():
    return flask.render_template('index.html', core=app.core)


@app.route('/check.json')
def check_json():
    ignore_fake = ('ignore_fake' in flask.request.args)

    if 'checks' in flask.request.args:
        checks = [x for x in app.core.checks
                  if x.name in flask.request.args['checks'].split(',')]
    else:
        checks = app.core.checks

    data = {
        'checks': [],
        'global_status': bleemeo_agent.checker.STATUS_GOOD,
    }
    for check in checks:
        check_info = {
            'check_name': check.name,
            'soft_status': check.soft_status,
            'hard_status': check.hard_status,
            'soft_status_try': check.soft_status_try,
            'fake': False,
        }
        if check.fake_failure_until and not ignore_fake:
            check_info.update({
                'soft_status': bleemeo_agent.checker.STATUS_CRITICAL,
                'hard_status': bleemeo_agent.checker.STATUS_CRITICAL,
                'soft_status_try': 4,
                'fake': True,
            })

        data['global_status'] = max(
            data['global_status'], check_info['hard_status'])
        data['checks'].append(check_info)

    response = flask.jsonify(**data)
    if data['global_status'] >= bleemeo_agent.checker.STATUS_CRITICAL:
        response.status_code = 500
    elif data['global_status'] == bleemeo_agent.checker.STATUS_WARNING:
        response.status_code = 400
    return response


@app.route('/admin', methods=['GET', 'POST'])
def admin():
    if flask.request.method == 'POST':
        action = flask.request.form.get('action')
        if action == 'refresh_plugins':
            if app.core.reload_plugins():
                return flask.render_template(
                    'restarting.html', core=app.core)
            else:
                flask.flash('Re-scan finished. List unchanged')
        elif action.startswith('restore-'):
            check_index0 = int(action[len('restore-'):])
            if check_index0 < len(app.core.checks):
                check = app.core.checks[check_index0]
                check.fake_failure_stop()
        elif action.startswith('fake-failure-'):
            check_index0 = int(action[len('fake-failure-'):])
            if check_index0 < len(app.core.checks):
                check = app.core.checks[check_index0]
                check.fake_failure_start()
        return flask.redirect(flask.url_for('admin'))

    return flask.render_template(
        'admin.html', core=app.core, now=time.time())


@app.route('/about')
def about():
    return flask.render_template('about.html', core=app.core)


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
