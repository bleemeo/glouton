import bleemeo_agent.telegraf


def test_compare_version():
    assert bleemeo_agent.telegraf.compare_version('1.0.0', '1.0.0')
    assert bleemeo_agent.telegraf.compare_version('1.0.0-beta3', '1.0.0')
    assert bleemeo_agent.telegraf.compare_version('1.0.0+bleemeo1-1', '1.0.0')

    assert not bleemeo_agent.telegraf.compare_version(
        '0.12.1+bleemeo1-1', '1.0.0'
    )
    assert not bleemeo_agent.telegraf.compare_version(
        '0.13.1', '1.0.0'
    )

    assert bleemeo_agent.telegraf.compare_version('1.0.1', '1.0.0')
    assert bleemeo_agent.telegraf.compare_version('1.1.0-beta1', '1.0.0')
    assert bleemeo_agent.telegraf.compare_version('2.0.0+bleemeo1-1', '1.0.0')
