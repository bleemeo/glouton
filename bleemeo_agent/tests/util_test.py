
import bleemeo_agent.util


def test_format_uptime():
    # edge case, uptime below 1 minute are shown are 0 minute.
    assert bleemeo_agent.util.format_uptime(59) == '0 minute'

    assert bleemeo_agent.util.format_uptime(1*60) == '1 minute'
    assert bleemeo_agent.util.format_uptime(42*60) == '42 minutes'
    # minutes are not shown when we switch to hours
    assert bleemeo_agent.util.format_uptime(2*60*60 + 5*60) == '2 hours'
    assert bleemeo_agent.util.format_uptime(
        2*24*60*60 + 1*60*60 + 5) == '2 days, 1 hour'
    assert bleemeo_agent.util.format_uptime(
        800*24*60*60 + 2*60*60 + 5) == '800 days, 2 hours'

    # Giving float instead of int also works well
    assert bleemeo_agent.util.format_uptime(float(42*60)) == '42 minutes'
    assert bleemeo_agent.util.format_uptime(float(2*60*60 + 5*60)) == '2 hours'
    assert bleemeo_agent.util.format_uptime(
        float(2*24*60*60 + 1*60*60 + 5)) == '2 days, 1 hour'
