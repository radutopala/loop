@frontend @math
Feature: Chat Math Journey
  LaTeX in agent replies renders as math: display formulas between $$ or \[
  and \], inline formulas between $ or \( and \). Dollar amounts in prose
  stay prose, and an escaped pipe keeps |x| inside one table cell.

  Scenario: Formulas in an agent reply render as math
    Given I set up a test channel via API for git repo "bdd-chat-math"
    And I open the app in a browser
    And I wait for text "bdd-chat-math" to appear

    When I click on "bdd-chat-math" in the sidebar
    And I wait for "textarea" to be visible
    And I inject a bot message with:
      """
      Math reply

      $$\frac{a}{b} = \sqrt{x}$$

      $$
      |A| = \begin{cases} A, & A \geq 0 \\ -A, & A < 0 \end{cases}
      $$

      So $x \in [-2, -1]$ and **$y^2$**, while it costs $5 and $10.

      | Modulus | Result |
      |---|---|
      | \|1 + x\| | $-1 - x$ |
      """
    Then I wait for text "Math reply" to appear

    # Both display formulas render, with KaTeX's own layout
    And I wait for "[data-testid='math-display'] .katex-display" to be visible
    And the page should not contain text "\frac"
    And the page should not contain text "\begin{cases}"

    # Inline formulas render in prose, in bold and in a table cell
    And I wait for "p .katex" to be visible
    And I wait for "strong .katex" to be visible
    And I wait for "td .katex" to be visible

    # Prices stay prose and the escaped pipes stay in their cell
    And the page should contain text "costs $5 and $10"
    And the element "td" should contain text "|1 + x|"
