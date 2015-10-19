import multiprocessing
import threading

import flask
import jinja2.filters
import requests

import bleemeo_agent.checker


app = flask.Flask(__name__)
app_thread = None


@app.route('/')
def home():
    loads = bleemeo_agent.util.get_loadavg()
    num_core = multiprocessing.cpu_count()
    check_info = _gather_checks_info()
    top_output = bleemeo_agent.util.get_top_output(app.core.top_info)
    disks_used_perc = [
        metric
        for metric in app.core.last_metrics.values()
        if metric['measurement'] == 'disk_used_perc'
    ]
    nets_bits_recv = [
        metric
        for metric in app.core.last_metrics.values()
        if metric['measurement'] == 'net_bits_recv'
    ]

    return flask.render_template(
        'index.html',
        core=app.core,
        loads=' '.join('%.2f' % x for x in loads),
        num_core=num_core,
        check_info=check_info,
        top_output=top_output,
        disks_used_perc=disks_used_perc,
        nets_bits_recv=nets_bits_recv,
    )


def _gather_checks_info():
    check_count_ok = 0
    check_count_warning = 0
    check_count_critical = 0
    checks = []
    for metric in app.core.last_metrics.values():
        if metric['status'] is not None:
            if metric['status'] == 'ok':
                check_count_ok += 1
            elif metric['status'] == 'warning':
                check_count_warning += 1
            else:
                check_count_critical += 1
            threshold = app.core.thresholds.get(metric['measurement'])

            pretty_name = metric['measurement']
            if metric['item'] is not None:
                pretty_name = '%s for %s' % (pretty_name, metric['item'])
            checks.append({
                'name': metric['measurement'],
                'pretty_name': pretty_name,
                'item': metric['item'],
                'status': metric['status'],
                'value': metric['value'],
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
    global app_thread

    bind_address = core.config.get(
        'web.listener.address', '127.0.0.1')
    bind_port = core.config.get(
        'web.listener.port', 8015)
    app.core = core
    if app.core.state.get('web_secret_key') is None:
        app.core.state.set(
            'web_secret_key', bleemeo_agent.util.generate_password())
    app.secret_key = app.core.state.get('web_secret_key')
    app_thread = threading.Thread(
        target=app.run,
        kwargs={'host': bind_address, 'port': bind_port}
    )
    app_thread.daemon = True
    app_thread.start()


def shutdown_server():
    bind_address = app.core.config.get(
        'web.listener.address', '127.0.0.1')

    if bind_address == '0.0.0.0':
        bind_address = '127.0.0.1'

    bind_port = app.core.config.get(
        'web.listener.port', 8015)
    requests.get('http://%s:%s/_quit' % (bind_address, bind_port))
    app_thread.join()
