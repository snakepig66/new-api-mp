import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MultiplierSlider } from './MultiplierSlider'

describe('MultiplierSlider', () => {
  afterEach(() => {
    cleanup()
  })

  it('renders slider, input and discount badge with 0.01 precision', () => {
    const onChange = vi.fn()
    render(<MultiplierSlider value={0.8} onChange={onChange} id="test-slider" />)

    const slider = screen.getByRole('slider') as HTMLInputElement
    const input = screen.getByLabelText('倍率') as HTMLInputElement

    expect(slider).toBeTruthy()
    expect(slider.min).toBe('0.01')
    expect(slider.max).toBe('1')
    expect(slider.step).toBe('0.01')
    expect(slider.value).toBe('0.8')

    expect(input.value).toBe('0.80')
    expect(screen.getByText('0.80x')).toBeTruthy()
  })

  it('renders multiplier and actual price when basePriceMicro is provided', () => {
    const onChange = vi.fn()
    render(<MultiplierSlider value={0.8} basePriceMicro={1_000_000} onChange={onChange} />)

    expect(screen.getByText('0.80x · ¥0.80/M')).toBeTruthy()
  })

  it('adjusts value on slider change with 0.01 precision', () => {
    const onChange = vi.fn()
    render(<MultiplierSlider value={0.8} onChange={onChange} />)

    const slider = screen.getByRole('slider')
    fireEvent.change(slider, { target: { value: '0.65' } })

    expect(onChange).toHaveBeenCalledWith(0.65, '0.65')
  })

  it('adjusts value on input typing', () => {
    const onChange = vi.fn()
    render(<MultiplierSlider value={0.8} onChange={onChange} />)

    const input = screen.getByLabelText('倍率')
    fireEvent.change(input, { target: { value: '0.75' } })

    expect(onChange).toHaveBeenCalledWith(0.75, '0.75')
  })

  it('adjusts value by ±0.01 on mouse wheel roll', () => {
    const onChange = vi.fn()
    const { container } = render(<MultiplierSlider value={0.8} onChange={onChange} />)

    const root = container.firstChild as HTMLElement
    expect(root).toBeTruthy()

    // Wheel UP (deltaY < 0) -> +0.01
    fireEvent.wheel(root, { deltaY: -100 })
    expect(onChange).toHaveBeenCalledWith(0.81, '0.81')

    // Wheel DOWN (deltaY > 0) -> -0.01
    fireEvent.wheel(root, { deltaY: 100 })
    expect(onChange).toHaveBeenCalledWith(0.79, '0.79')
  })

  it('clamps mouse wheel to min and max bounds', () => {
    const onChange = vi.fn()
    const { container } = render(
      <MultiplierSlider value={1.0} min={0.01} max={1.0} onChange={onChange} />,
    )

    const root = container.firstChild as HTMLElement
    // At max: wheel UP cannot exceed max 1.00
    fireEvent.wheel(root, { deltaY: -100 })
    expect(onChange).not.toHaveBeenCalled()
  })
})
