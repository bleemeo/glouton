import multiprocessing
import threading

import flask
import jinja2.filters
import requests

import bleemeo_agent.checker


app = flask.Flask(__name__)
app_thread = threading.Thread(target=app.run)


@app.route('/')
def home():
    loads = bleemeo_agent.util.get_loadavg()
    num_core = multiprocessing.cpu_count()
    check_info = _gather_checks_info()
    top_output = bleemeo_agent.util.get_top_output(app.core.top_info)

    return flask.render_template(
        'index.html',
        core=app.core,
        loads=' '.join('%.2f' % x for x in loads),
        num_core=num_core,
        check_info=check_info,
        top_output=top_output,
    )


def _gather_checks_info():
    check_count_ok = 0
    check_count_warning = 0
    check_count_critical = 0
    checks = []
    for metrics in app.core.last_metrics.values():
        for metric in metrics:
            if 'status' in metric['tags']:
                if metric['tags']['status'] == 'ok':
                    check_count_ok += 1
                elif metric['tags']['status'] == 'warning':
                    check_count_warning += 1
                else:
                    check_count_critical += 1
                threshold = app.core.thresholds.get(metric['measurement'])

                tags = metric['tags'].copy()
                del tags['status']

                pretty_name = metric['measurement']
                for (key, value) in tags.items():
                    pretty_name = '%s for %s %s' % (pretty_name, key, value)
                checks.append({
                    'name': metric['measurement'],
                    'pretty_name': pretty_name,
                    'tags': tags,
                    'status': metric['tags']['status'],
                    'value': metric['fields'].get('value'),
                    'threshold': threshold,
                })

    return {
        'checks': checks,
        'count_ok':  check_count_ok,
        'count_warning': check_count_warning,
        'count_critical': check_count_critical,
        'count_total': len(checks),
    }


@app.route('/check')
def check():
    check_info = _gather_checks_info()

    return flask.render_template(
        'check.html',
        core=app.core,
        check_info=check_info,
    )


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


@app.template_filter('netsizeformat')
def filter_netsizeformat(value):
    """ Same as standard filesizeformat but for network.

        Convert to human readable network bandwidth (e.g 13 kbps, 4.1 Mbps...)
    """
    return (jinja2.filters.do_filesizeformat(value * 8, False)
            .replace('Bytes', 'bps')
            .replace('B', 'bps'))


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
